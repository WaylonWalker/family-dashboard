/* Minimal ES2017-free controller for Android 6 / older WebView kiosks. */
(function () {
  'use strict';
  var root = document.documentElement;
  var body = document.body;
  var panel = document.getElementById('feature-window');
  var sharedCalendar = document.querySelector('.calendar');
  var sharedTaskPane = document.getElementById('pane-tasks');
  var sharedTaskTab = document.getElementById('tab-tasks');
  var panes = [document.getElementById('pane-weather'), document.getElementById('pane-tasks'), document.getElementById('pane-cameras')];
  var tabs = [document.getElementById('tab-weather'), document.getElementById('tab-tasks'), document.getElementById('tab-cameras')];
  var paneBaseClasses = ['feature-pane','feature-pane','feature-pane cameras-overview'];
  var pager = document.querySelectorAll('.pager-dot');
  var rotationProgress = document.getElementById('rotation-progress');
  var name = document.getElementById('feature-title');
  var hint = document.getElementById('rotation-hint');
  var drawer = document.getElementById('settings');
  var backdrop = document.getElementById('settings-backdrop');
  var launch = document.getElementById('open-settings');
  var theme = document.getElementById('theme-select');
  var texture = document.getElementById('texture-select');
  var font = document.getElementById('font-select');
  var style = document.getElementById('style-select');
  var rotate = document.getElementById('rotate-toggle');
  var wakeLockToggle = document.getElementById('wake-lock-toggle');
  var wakeLockStatus = document.getElementById('wake-lock-status');
  var isKiosk = body.getAttribute('data-kiosk') === 'true';
  var wideDisplay = isKiosk || (window.matchMedia ? window.matchMedia('(min-width:1200px) and (orientation:landscape)').matches : window.innerWidth >= 1200);
  var cameraCycleTimer = null;
  var cameraWarmTimer = null;
  var cameraList = [];
  var cameraIndex = 0;
  var cameraTiles = [];
  var cameraSessionGeneration = 0;
  var cameraRefreshGeneration = 0;
  var cameraSnapshotTimer = null;
  var headerSensorRefreshGeneration = 0;
  var activeSensorAlerts = {temperature:false, water:false};
  var cameraProgressTimer = null;
  var cameraCycleStartedAt = 0;
  var wakeLockSentinel = null;
  var wakeLockRequestId = 0;
  var wakeLockPending = false;
  var wakeLockRetryTimer = null;
  var active = 0;
  var holdUntil = 0;
  var rotationStartedAt = 0;
  var rotationProgressCompletedAt = 0;
  var cameraPanelShownAt = 0;
  var lastFocus = null;
  var settingsCloseTimer = null;

  function recordCameraTelemetry(eventName, attributes) {
    try {
      var faro = window.__waylonFaro;
      if (faro && faro.api && typeof faro.api.pushEvent === 'function') {
        faro.api.pushEvent(eventName, attributes || {});
      }
    } catch (error) { /* Browser telemetry must never interrupt the camera UI. */ }
  }

  function snapshotTransferStats(image) {
    var stats = { natural_width: image.naturalWidth || 0, natural_height: image.naturalHeight || 0 };
    try {
      var url = image.currentSrc || image.src;
      var entries = window.performance && window.performance.getEntriesByName(url);
      if (entries && entries.length) {
        var entry = entries[entries.length - 1];
        stats.transfer_size = entry.transferSize || 0;
        stats.encoded_body_size = entry.encodedBodySize || 0;
        stats.decoded_body_size = entry.decodedBodySize || 0;
      }
    } catch (error) { /* Resource timing may be unavailable in older browsers. */ }
    return stats;
  }

  function observeMainThreadStalls() {
    if (!window.PerformanceObserver) return;
    try {
      var count = 0;
      var totalMs = 0;
      var maxMs = 0;
      var lastSentAt = 0;
      var observer = new PerformanceObserver(function (list) {
        var entries = list.getEntries();
        for (var i = 0; i < entries.length; i++) {
          count++;
          totalMs += entries[i].duration;
          maxMs = Math.max(maxMs, entries[i].duration);
        }
        var now = window.performance.now();
        if (!count || (lastSentAt && now - lastSentAt < 15000)) return;
        recordCameraTelemetry('browser_main_thread_stalls', {
          interval_ms: Math.round(lastSentAt ? now - lastSentAt : now),
          count: count,
          total_duration_ms: Math.round(totalMs),
          max_duration_ms: Math.round(maxMs),
          visibility_state: document.visibilityState || 'unknown'
        });
        lastSentAt = now;
        count = 0;
        totalMs = 0;
        maxMs = 0;
      });
      observer.observe({ entryTypes: ['longtask'] });
    } catch (error) { /* Long-task entries are not supported on every kiosk. */ }
  }
  observeMainThreadStalls();

  function read(key, fallback) {
    try { return window.localStorage.getItem('familyhq.' + key) || fallback; }
    catch (e) { return fallback; }
  }
  function save(key, value) {
    try { window.localStorage.setItem('familyhq.' + key, String(value)); }
    catch (e) { /* Settings still work without storage. */ }
  }
  var themes = ['auto','everforest-light','everforest-dark','catppuccin-latte','catppuccin-mocha','gruvbox-light','gruvbox-dark','kanagawa-lotus','kanagawa-wave'];
  function allowed(value, list, fallback) { return list.indexOf(value) === -1 ? fallback : value; }
  theme.value = allowed(read('theme', 'auto'), themes, 'auto');
  texture.value = allowed(read('texture', 'paper'), ['paper','screenprint','halftone','none'], 'paper');
  font.value = allowed(read('font', 'system'), ['system','editorial','modern','mono'], 'system');
  style.value = allowed(read('style', 'balanced'), ['balanced','precision','elevated','brutal','minimal'], 'balanced');
  rotate.checked = read('rotate', 'true') !== 'false';
  wakeLockToggle.checked = read('keep-awake', isKiosk ? 'true' : 'false') === 'true';

  function scheduleWakeLockRetry() {
    if (!wakeLockToggle.checked || document.hidden || wakeLockRetryTimer !== null) return;
    wakeLockRetryTimer = window.setTimeout(function () {
      wakeLockRetryTimer = null;
      requestWakeLock();
    }, 30000);
  }

  function requestWakeLock() {
    if (!wakeLockToggle.checked) return;
    if (wakeLockSentinel || wakeLockPending) return;
    if (!('wakeLock' in navigator)) {
      wakeLockStatus.textContent = 'Unavailable · use Fire TV display settings to prevent sleep.';
      return;
    }
    if (document.hidden) {
      wakeLockStatus.textContent = 'Waiting · the page must be visible to keep the screen awake.';
      return;
    }
    var requestId = ++wakeLockRequestId;
    wakeLockPending = true;
    wakeLockStatus.textContent = 'Requesting screen wake lock…';
    navigator.wakeLock.request('screen').then(function (sentinel) {
      wakeLockPending = false;
      if (requestId !== wakeLockRequestId || !wakeLockToggle.checked) {
        sentinel.release();
        return;
      }
      wakeLockSentinel = sentinel;
      wakeLockStatus.textContent = 'On · this browser is keeping the screen awake.';
      sentinel.addEventListener('release', function () {
        var wasCurrent = wakeLockSentinel === sentinel;
        if (wasCurrent) wakeLockSentinel = null;
        if (wasCurrent && wakeLockToggle.checked && !document.hidden) {
          wakeLockStatus.textContent = 'Wake lock released · trying again…';
          scheduleWakeLockRetry();
        }
      });
    }).catch(function () {
      wakeLockPending = false;
      if (requestId !== wakeLockRequestId || !wakeLockToggle.checked) return;
      wakeLockStatus.textContent = 'Not granted · retrying; check Fire TV sleep settings if it keeps sleeping.';
      scheduleWakeLockRetry();
    });
  }

  function releaseWakeLock() {
    wakeLockRequestId++;
    wakeLockPending = false;
    if (wakeLockSentinel) {
      var sentinel = wakeLockSentinel;
      wakeLockSentinel = null;
      sentinel.release();
    }
  }

  wakeLockToggle.addEventListener('change', function () {
    save('keep-awake', String(wakeLockToggle.checked));
    if (wakeLockToggle.checked) requestWakeLock();
    else {
      releaseWakeLock();
      wakeLockStatus.textContent = 'Off · screen sleep follows Fire TV settings.';
    }
  });
  document.addEventListener('visibilitychange', function () {
    if (document.hidden) {
      if (wakeLockRetryTimer !== null) window.clearTimeout(wakeLockRetryTimer);
      wakeLockRetryTimer = null;
      releaseWakeLock();
    } else if (wakeLockToggle.checked) requestWakeLock();
  });
  window.addEventListener('pageshow', function () {
    if (wakeLockToggle.checked) requestWakeLock();
  });
  window.addEventListener('focus', function () {
    if (wakeLockToggle.checked) requestWakeLock();
  });

  function applyTheme() {
    var selected = theme.value;
    var description = 'Manual palette: ' + selected.replace(/-/g, ' ') + '.';
    if (selected === 'auto') {
      var hour = new Date().getHours();
      var weather = body.getAttribute('data-weather') || '';
      var rainy = weather === 'rain' || weather === 'storm' || weather === 'snow';
      if (hour < 6 || hour >= 20) selected = rainy ? 'kanagawa-wave' : 'catppuccin-mocha';
      else if (hour < 9 || hour >= 17) selected = rainy ? 'gruvbox-dark' : 'everforest-dark';
      else selected = rainy ? 'kanagawa-lotus' : 'everforest-light';
      description = 'Auto · ' + (hour < 6 || hour >= 20 ? 'night' : hour < 9 || hour >= 17 ? 'golden hours' : 'daylight') + (rainy ? ' + wet weather' : '') + ' → ' + selected.replace(/-/g,' ');
    }
    root.setAttribute('data-theme', selected);
    var themeColor = getComputedStyle(root).getPropertyValue('--bg').trim();
    var themeColorMeta = document.querySelector('meta[name="theme-color"]');
    if (themeColorMeta && themeColor) themeColorMeta.setAttribute('content', themeColor);
    root.setAttribute('data-texture', texture.value);
    root.setAttribute('data-font', font.value);
    root.setAttribute('data-style', style.value);
    document.getElementById('auto-description').textContent = description;
  }
  theme.addEventListener('change', function () { save('theme',theme.value); applyTheme(); });
  texture.addEventListener('change', function () { save('texture',texture.value); applyTheme(); });
  font.addEventListener('change', function () { save('font',font.value); applyTheme(); });
  style.addEventListener('change', function () { save('style',style.value); applyTheme(); });
  rotate.addEventListener('change', function () { save('rotate',String(rotate.checked)); refreshHint(); });
  document.getElementById('reset-settings').addEventListener('click', function () {
    theme.value='auto'; texture.value='paper'; font.value='system'; style.value='balanced'; rotate.checked=true; wakeLockToggle.checked=isKiosk;
    save('theme','auto');save('texture','paper');save('font','system');save('style','balanced');save('rotate','true');save('keep-awake',String(isKiosk));releaseWakeLock();wakeLockStatus.textContent=isKiosk?'Requesting screen wake lock…':'Off · screen sleep follows Fire TV settings.';if(isKiosk)requestWakeLock();applyTheme();refreshHint();
  });
  applyTheme();
  if (wakeLockToggle.checked) requestWakeLock();
  function updateClock() {
    try {
      var formattedTime = new Intl.DateTimeFormat('en-US',{timeZone:'America/Chicago',hour:'numeric',minute:'2-digit'}).format(new Date());
      var timeParts = formattedTime.match(/^(\d{1,2}:\d{2})\s*(AM|PM)$/i);
      var date = new Intl.DateTimeFormat('en-US',{timeZone:'America/Chicago',weekday:'short',month:'short',day:'numeric'}).format(new Date());
      document.getElementById('live-clock').textContent=timeParts ? timeParts[1] : formattedTime;
      if (timeParts) document.getElementById('live-period').textContent=timeParts[2];
      document.getElementById('live-date').textContent=date;
    } catch(e) { /* Keep server-rendered local clock on unsupported devices. */ }
  }
  function updateEventTimes() {
    var labels = document.querySelectorAll('.event-relative');
    var leaveLabels = document.querySelectorAll('.leave-countdown');
    var schoolTimers = document.querySelectorAll('[data-countdown-at]');
    var schoolAlerts = document.querySelectorAll('[data-alert-start]');
    var units = [
      [365.25 * 86400, 'y'], [30.44 * 86400, 'mo'], [7 * 86400, 'w'],
      [86400, 'd'], [3600, 'h'], [60, 'm'], [1, 's']
    ];
    var now = Date.now();
    for (var a = 0; a < schoolAlerts.length; a++) {
      var starts = Date.parse(schoolAlerts[a].getAttribute('data-alert-start'));
      var ends = Date.parse(schoolAlerts[a].getAttribute('data-alert-end'));
      if (isFinite(starts) && isFinite(ends)) schoolAlerts[a].hidden = now < starts || now >= ends;
    }
    for (var s = 0; s < schoolTimers.length; s++) {
      var secondsLeft = Math.ceil((Date.parse(schoolTimers[s].getAttribute('data-countdown-at')) - now) / 1000);
      if (!isFinite(secondsLeft)) continue;
      if (secondsLeft <= 0) schoolTimers[s].textContent = 'NOW';
      else if (secondsLeft < 3600) schoolTimers[s].textContent = Math.ceil(secondsLeft / 60) + 'm';
      else schoolTimers[s].textContent = Math.floor(secondsLeft / 3600) + 'h' + (Math.floor((secondsLeft % 3600) / 60) ? ' ' + Math.floor((secondsLeft % 3600) / 60) + 'm' : '');
    }
    var outing = document.querySelector('.next-up[data-travel-phase]');
    if (outing) {
      var startAt = Date.parse(outing.getAttribute('data-travel-start'));
      var endAt = Date.parse(outing.getAttribute('data-travel-end'));
      var phase = outing.getAttribute('data-travel-phase');
      if ((phase === 'outbound' && now >= startAt) || (phase === 'pickup' && now >= endAt)) {
        window.location.reload();
        return;
      }
    }
    for (var i = 0; i < labels.length; i++) {
      var seconds = (Date.parse(labels[i].getAttribute('data-start')) - now) / 1000;
      if (!isFinite(seconds)) continue;
      if (Math.abs(seconds) < 1) { labels[i].textContent = 'now'; continue; }
      var amount = Math.abs(seconds);
      for (var u = 0; u < units.length; u++) {
        if (amount >= units[u][0] || u === units.length - 1) {
          var count = Math.max(1, Math.round(amount / units[u][0]));
          labels[i].textContent = seconds > 0 ? 'in ' + count + units[u][1] : count + units[u][1] + ' ago';
          break;
        }
      }
    }
    for (var j = 0; j < leaveLabels.length; j++) {
      var remaining = Math.ceil((Date.parse(leaveLabels[j].getAttribute('data-leave-at')) - now) / 1000);
      if (!isFinite(remaining)) continue;
      var card = leaveLabels[j].parentNode.parentNode;
      if (card && card.classList) {
        if (remaining <= 3600) card.classList.add('is-approaching');
        else card.classList.remove('is-approaching');
        if (remaining > 86400) card.classList.add('is-distant');
        else card.classList.remove('is-distant');
      }
      var text = 'NOW';
      if (remaining > 0) {
        if (remaining < 60) text = remaining + 's';
        else {
          var mins = Math.floor(remaining / 60);
          if (mins < 60) text = mins + 'm';
          else {
            var hours = Math.floor(mins / 60);
            if (hours < 24) text = hours + 'h' + (mins % 60 ? ' ' + (mins % 60) + 'm' : '');
            else {
              var days = Math.floor(hours / 24);
              text = days + 'd' + (hours % 24 ? ' ' + (hours % 24) + 'h' : '');
            }
          }
        }
      }
      leaveLabels[j].textContent = text;
      var caption = leaveLabels[j].parentNode.querySelector('span');
      if (caption) {
        var pickup = leaveLabels[j].classList.contains('pickup-countdown');
        caption.textContent = pickup
          ? (remaining <= 0 ? 'Leave for pickup now' : 'Leave for pickup in')
          : (remaining <= 0 ? 'Leave now' : 'Leave in');
      }
    }
  }
  updateClock();
  refreshHeaderSensors();
  updateEventTimes();
  window.setInterval(updateEventTimes, 1000);
  window.setInterval(function () { if (theme.value === 'auto') applyTheme(); updateClock(); }, 60000);
  window.setInterval(refreshHeaderSensors, 60000);

  function resetRotationProgress() {
    rotationStartedAt = 0;
    rotationProgress.hidden = true;
    rotationProgress.style.transform = 'scaleX(0)';
    rotationProgress.setAttribute('aria-valuenow', '0');
    rotationProgress.setAttribute('aria-valuetext', 'Auto-switch paused');
  }
  function pause() { holdUntil = Date.now() + 120000; resetRotationProgress(); refreshHint(); }
  function panelRotationDuration() {
    if (active !== 2 || !wideDisplay || cameraList.length <= 4) return 30000;
    return Math.ceil(cameraList.length / 4) * 30000;
  }
  function requestJSON(path, done) {
    var request = new XMLHttpRequest();
    request.open('GET', path, true);
    request.setRequestHeader('Accept', 'application/json');
    request.onload = function () {
      if (request.status < 200 || request.status >= 300) { done(new Error('Request failed')); return; }
      try { done(null, JSON.parse(request.responseText)); }
      catch (_) { done(new Error('Invalid response')); }
    };
    request.onerror = function () { done(new Error('Network error')); };
    request.send();
  }
  function updateHeaderSensors(error, readings) {
    var freezer = document.getElementById('header-freezer');
    var basement = document.getElementById('header-basement');
    var freezerValue = freezer.querySelector('strong');
    var basementValue = basement.querySelector('strong');
    var freezerSignal = freezer.querySelector('.sensor-signal');
    var basementSignal = basement.querySelector('.sensor-signal');
    function setConnection(node, signal, reading) {
      var online = null;
      if (reading && typeof reading.online === 'boolean') online = reading.online;
      else if (reading && reading.updated_at) {
        var lastSeen = new Date(reading.updated_at).getTime();
        if (!isNaN(lastSeen) && Date.now() - lastSeen < 30 * 60 * 1000) online = true;
      }
      node.classList.toggle('is-online', online === true);
      node.classList.toggle('is-offline', online === false);
      signal.setAttribute('aria-label', online === true ? 'Online' : online === false ? 'Offline' : 'Last reported; current status unknown');
      node.title = node.textContent.trim() + ' sensor ' + (online === true ? 'online' : online === false ? 'offline' : 'last reported; current status unknown');
      return online;
    }
    freezer.classList.remove('is-alert');
    basement.classList.remove('is-alert');
    if (error) {
      freezerValue.textContent = 'Unknown';
      basementValue.textContent = 'Unknown';
      setConnection(freezer, freezerSignal, null);
      setConnection(basement, basementSignal, null);
      if (activeSensorAlerts.temperature) freezer.classList.add('is-alert');
      if (activeSensorAlerts.water) basement.classList.add('is-alert');
      return;
    }
    var temperature = null;
    var water = null;
    (readings || []).forEach(function (reading) {
      if (reading.kind === 'temperature') temperature = reading;
      if (reading.kind === 'water') water = reading;
    });
    var freezerOnline = setConnection(freezer, freezerSignal, temperature);
    var basementOnline = setConnection(basement, basementSignal, water);
    freezerValue.textContent = !temperature ? 'Unknown' : freezerOnline === false ? 'Offline' : typeof temperature.temperature_f === 'number' ? temperature.temperature_f.toFixed(1) + '°F' : 'Unknown';
    basementValue.textContent = !water ? 'Unknown' : basementOnline === false ? 'Offline' : water.detected ? 'Wet' : 'Dry';
    if (water && water.detected) basement.classList.add('is-alert');
    if (activeSensorAlerts.temperature) freezer.classList.add('is-alert');
    if (activeSensorAlerts.water) basement.classList.add('is-alert');
  }
  function refreshHeaderSensors() {
    var refreshGeneration = ++headerSensorRefreshGeneration;
    var essentialsBase = isKiosk ? '/kiosk/api/essentials' : '/api/essentials';
    requestJSON(essentialsBase + '/sensors', function (error, data) {
      if (refreshGeneration !== headerSensorRefreshGeneration) return;
      updateHeaderSensors(error, data && data.readings);
    });
  }
  function updateHeaderSensorFromEvent(event) {
    if (event.severity === 'test') return;
    var node = document.getElementById(event.kind === 'water' ? 'header-basement' : 'header-freezer');
    var signal = node.querySelector('.sensor-signal');
    var value = node.querySelector('strong');
    activeSensorAlerts[event.kind] = Boolean(event.active);
    node.classList.add('is-online');
    node.classList.remove('is-offline');
    signal.setAttribute('aria-label', 'Online');
    node.title = node.textContent.trim() + ' sensor online';
    if (event.kind === 'water') {
      value.textContent = event.active ? 'Wet' : 'Dry';
      node.classList.toggle('is-alert', Boolean(event.active));
    } else {
      if (typeof event.temperature_f === 'number') value.textContent = event.temperature_f.toFixed(1) + '°F';
      node.classList.toggle('is-alert', Boolean(event.active));
    }
    refreshHeaderSensors();
  }
  var sharedRefreshPending = false;
  var sharedRefreshTimer = null;
  function isEditingSharedContent() {
    var focused = document.activeElement;
    if (!focused || !/^(INPUT|TEXTAREA|SELECT)$/.test(focused.tagName)) return false;
    return sharedCalendar.contains(focused) || sharedTaskPane.contains(focused);
  }
  function scheduleSharedRefresh() {
    if (sharedRefreshTimer !== null) window.clearTimeout(sharedRefreshTimer);
    sharedRefreshTimer = window.setTimeout(refreshSharedContent, 250);
  }
  function refreshSharedContent() {
    sharedRefreshTimer = null;
    if (isEditingSharedContent()) {
      sharedRefreshPending = true;
      return;
    }
    var request = new XMLHttpRequest();
    request.open('GET', window.location.pathname, true);
    request.setRequestHeader('Cache-Control', 'no-cache');
    request.onload = function () {
      if (request.status < 200 || request.status >= 300) return;
      if (isEditingSharedContent()) {
        sharedRefreshPending = true;
        return;
      }
      try {
        var latest = new DOMParser().parseFromString(request.responseText, 'text/html');
        var latestCalendar = latest.querySelector('.calendar');
        var latestTasks = latest.getElementById('pane-tasks');
        var latestTaskTab = latest.getElementById('tab-tasks');
        if (!latestCalendar || !latestTasks || !latestTaskTab) return;
        sharedCalendar.innerHTML = latestCalendar.innerHTML;
        sharedTaskPane.innerHTML = latestTasks.innerHTML;
        var currentCount = sharedTaskTab.querySelector('.pill-count');
        var latestCount = latestTaskTab.querySelector('.pill-count');
        if (currentCount && latestCount) currentCount.textContent = latestCount.textContent;
        sharedRefreshPending = false;
      } catch (error) { /* Keep the current display if its browser cannot parse refreshed markup. */ }
    };
    request.send();
  }
  document.addEventListener('focusout', function () {
    if (sharedRefreshPending && !isEditingSharedContent()) scheduleSharedRefresh();
  }, true);
  function stopCameraRotation() {
    cameraSessionGeneration++;
    if (cameraCycleTimer !== null) window.clearInterval(cameraCycleTimer);
    cameraCycleTimer = null;
    if (cameraWarmTimer !== null) window.clearTimeout(cameraWarmTimer);
    cameraWarmTimer = null;
    if (cameraProgressTimer !== null) window.clearInterval(cameraProgressTimer);
    cameraProgressTimer = null;
  }
  function stopCameraStreams() {
    if (cameraSnapshotTimer !== null) window.clearInterval(cameraSnapshotTimer);
    cameraSnapshotTimer = null;
    for (var i = 0; i < cameraTiles.length; i++) {
      var faces = cameraTiles[i].querySelectorAll('.camera-face');
      for (var j = 0; j < faces.length; j++) clearCameraFace(faces[j]);
    }
    cameraTiles = [];
  }
  function stopCameraPlayer(video) {
    if (!video) return;
    if (video._cameraStartupTimer !== null && video._cameraStartupTimer !== undefined) {
      window.clearTimeout(video._cameraStartupTimer);
      video._cameraStartupTimer = null;
    }
    if (video._cameraSocket) {
      video._cameraSocket.onclose = null;
      video._cameraSocket.onerror = null;
      video._cameraSocket.close();
      video._cameraSocket = null;
    }
    if (video._cameraObjectURL) {
      URL.revokeObjectURL(video._cameraObjectURL);
      video._cameraObjectURL = '';
    }
    video.removeAttribute('src');
    video.load();
  }
  function clearCameraFace(face) {
    if (!face) return;
    stopCameraPlayer(face.querySelector('video'));
    face.textContent = '';
  }
  function snapshotURL(camera, image) {
    var height = image.getBoundingClientRect().height || 360;
    height = Math.ceil(height);
    height = Math.max(120, Math.min(1080, height));
    return (isKiosk ? '/kiosk/api/essentials' : '/api/essentials') + '/cameras/' + encodeURIComponent(camera) + '/snapshot?height=' + height + '&quality=70&refresh=' + Date.now();
  }
  function loadCameraSnapshot(image) {
    if (!image || !image.getAttribute('data-camera')) return;
    image._cameraLoadStartedAt = Date.now();
    image._cameraLoadFace = image.parentNode && image.parentNode.className.indexOf('camera-face-back') !== -1 ? 'back' : 'front';
    image.src = snapshotURL(image.getAttribute('data-camera'), image);
  }
  function refreshCameraSnapshots() {
    if (active !== 2 || document.hidden) return;
    var images = document.querySelectorAll('#camera-grid .camera-face-front .camera-snapshot-live');
    for (var i = 0; i < images.length; i++) {
      var camera = images[i].getAttribute('data-camera');
      if (!camera) continue;
      var requestId = (images[i]._cameraRefreshRequest || 0) + 1;
      images[i]._cameraRefreshRequest = requestId;
      images[i]._cameraRefreshPending = true;
      if (images[i]._cameraLoadingTimer !== null && images[i]._cameraLoadingTimer !== undefined) window.clearTimeout(images[i]._cameraLoadingTimer);
      images[i]._cameraLoadingTimer = window.setTimeout(function (image, id) {
        return function () {
          if (image._cameraRefreshRequest === id && image._cameraRefreshPending && image._cameraRefreshIndicator) image._cameraRefreshIndicator.classList.add('is-active');
        };
      }(images[i], requestId), 120);
      images[i].src = snapshotURL(camera, images[i]);
    }
  }
  function showCameraSnapshotFallback(video, camera) {
    if (!video || !video.parentNode || video._cameraFallback) return;
    video._cameraFallback = true;
    if (video._cameraConnectStartedAt && !video._cameraFallbackReported) {
      video._cameraFallbackReported = true;
      recordCameraTelemetry('camera_stream_fallback', {
        duration_ms: Date.now() - video._cameraConnectStartedAt,
        face: video._cameraFace || 'unknown'
      });
    }
    stopCameraPlayer(video);
    var image = document.createElement('img');
    image.setAttribute('data-camera', camera);
    image.alt = camera.replace(/[-_]/g, ' ') + ' camera snapshot; live stream unavailable';
    video.parentNode.replaceChild(image, video);
    loadCameraSnapshot(image);
    var caption = video.parentNode.querySelector('figcaption');
    if (caption) caption.textContent = camera.replace(/[-_]/g, ' ') + ' · snapshot';
  }
  function connectCamera(video, camera) {
    if (!window.MediaSource || !window.WebSocket) return;
    video._cameraConnectStartedAt = Date.now();
    video._cameraFace = video.parentNode && video.parentNode.className.indexOf('camera-face-back') !== -1 ? 'back' : 'front';
    recordCameraTelemetry('camera_stream_connect_started', { face: video._cameraFace });
    var base = (isKiosk ? '/kiosk/api/essentials' : '/api/essentials') + '/cameras/' + encodeURIComponent(camera) + '/live';
    var socketURL = (window.location.protocol === 'https:' ? 'wss://' : 'ws://') + window.location.host + base;
    var socket = new WebSocket(socketURL);
    video._cameraSocket = socket;
    socket.binaryType = 'arraybuffer';
    var mediaSource = null;
    var sourceBuffer = null;
    var chunks = [];
    var codecCandidates = ['avc1.640029','avc1.64002A','avc1.640033','hvc1.1.6.L153.B0'];
    var supportedCodecs = [];
    if (typeof MediaSource.isTypeSupported === 'function') {
      for (var codecIndex = 0; codecIndex < codecCandidates.length; codecIndex++) {
        if (MediaSource.isTypeSupported('video/mp4; codecs="' + codecCandidates[codecIndex] + '"')) supportedCodecs.push(codecCandidates[codecIndex]);
      }
    }
    video._cameraStartupTimer = window.setTimeout(function () {
      if (video.readyState < 2) showCameraSnapshotFallback(video, camera);
    }, 12000);
    video.addEventListener('loadeddata', function () {
      if (video._cameraStartupTimer !== null) window.clearTimeout(video._cameraStartupTimer);
      video._cameraStartupTimer = null;
      recordCameraTelemetry('camera_stream_first_frame', {
        duration_ms: Date.now() - video._cameraConnectStartedAt,
        face: video._cameraFace,
        width: video.videoWidth || 0,
        height: video.videoHeight || 0
      });
    }, {once:true});
    video.addEventListener('error', function () { showCameraSnapshotFallback(video, camera); }, {once:true});
    function appendNext() {
      if (!sourceBuffer || sourceBuffer.updating || !chunks.length) return;
      try { sourceBuffer.appendBuffer(chunks.shift()); }
      catch (error) { showCameraSnapshotFallback(video, camera); }
    }
    socket.onopen = function () {
      recordCameraTelemetry('camera_stream_socket_open', {
        duration_ms: Date.now() - video._cameraConnectStartedAt,
        face: video._cameraFace
      });
      if (!supportedCodecs.length) { showCameraSnapshotFallback(video, camera); return; }
      socket.send(JSON.stringify({type:'mse',value:supportedCodecs.join(',')}));
    };
    socket.onmessage = function (event) {
      if (typeof event.data === 'string') {
        var message;
        try { message = JSON.parse(event.data); } catch (error) { return; }
        if (message.type !== 'mse' || !message.value) { showCameraSnapshotFallback(video, camera); return; }
        if (mediaSource) return;
        mediaSource = new MediaSource();
        video._cameraObjectURL = URL.createObjectURL(mediaSource);
        video.src = video._cameraObjectURL;
        mediaSource.addEventListener('sourceopen', function () {
          if (socket.readyState !== WebSocket.OPEN) return;
          try {
            sourceBuffer = mediaSource.addSourceBuffer(message.value);
            sourceBuffer.mode = 'segments';
            sourceBuffer.addEventListener('updateend', function () {
              if (sourceBuffer.buffered.length && !sourceBuffer.updating) {
                var end = sourceBuffer.buffered.end(sourceBuffer.buffered.length - 1);
                var start = sourceBuffer.buffered.start(sourceBuffer.buffered.length - 1);
                if (end - start > 15) {
                  try { sourceBuffer.remove(start, end - 8); return; } catch (error) {}
                }
                var liveStart = Math.max(start, end - 5);
                if (mediaSource.setLiveSeekableRange) mediaSource.setLiveSeekableRange(liveStart, end);
                if (video.currentTime < liveStart || video.currentTime > end) video.currentTime = liveStart;
              }
              appendNext();
            });
            var playResult = video.play();
            if (playResult && playResult.catch) playResult.catch(function () {});
            appendNext();
          } catch (error) { socket.close(); }
        }, {once:true});
        return;
      }
      if (event.data instanceof ArrayBuffer) {
        chunks.push(event.data);
        appendNext();
      }
    };
    socket.onerror = function () { showCameraSnapshotFallback(video, camera); };
    socket.onclose = function () { if (video.isConnected && video._cameraSocket === socket) showCameraSnapshotFallback(video, camera); };
  }
  function makeCameraFace(faceClass, camera) {
    var face = document.createElement('div');
    face.className = 'camera-face ' + faceClass;
    if (!camera) return face;
    face._cameraIndex = cameraList.indexOf(camera);
    if (wideDisplay || !window.MediaSource || !window.WebSocket) {
      var fallback = document.createElement('img');
      if (wideDisplay) {
        fallback.className = 'camera-snapshot-live';
        fallback.setAttribute('data-camera', camera);
      }
      var refreshIndicator = null;
      if (wideDisplay) {
        refreshIndicator = document.createElement('span');
        refreshIndicator.className = 'camera-fetch-indicator';
        refreshIndicator.setAttribute('aria-hidden', 'true');
        fallback._cameraRefreshIndicator = refreshIndicator;
        fallback.addEventListener('load', function () {
          fallback._cameraRefreshPending = false;
          if (fallback._cameraLoadingTimer !== null && fallback._cameraLoadingTimer !== undefined) window.clearTimeout(fallback._cameraLoadingTimer);
          refreshIndicator.classList.remove('is-active');
          if (fallback._cameraLoadStartedAt) {
            var transfer = snapshotTransferStats(fallback);
            recordCameraTelemetry('camera_snapshot_loaded', {
              duration_ms: Date.now() - fallback._cameraLoadStartedAt,
              face: fallback._cameraLoadFace || 'unknown',
              natural_width: transfer.natural_width,
              natural_height: transfer.natural_height,
              transfer_size: transfer.transfer_size,
              encoded_body_size: transfer.encoded_body_size,
              decoded_body_size: transfer.decoded_body_size
            });
          }
          fallback._cameraLoadStartedAt = 0;
        });
        fallback.addEventListener('error', function () {
          fallback._cameraRefreshPending = false;
          if (fallback._cameraLoadingTimer !== null && fallback._cameraLoadingTimer !== undefined) window.clearTimeout(fallback._cameraLoadingTimer);
          refreshIndicator.classList.remove('is-active');
          if (fallback._cameraLoadStartedAt) {
            recordCameraTelemetry('camera_snapshot_failed', {
              duration_ms: Date.now() - fallback._cameraLoadStartedAt,
              face: fallback._cameraLoadFace || 'unknown'
            });
          }
          fallback._cameraLoadStartedAt = 0;
        });
      }
      fallback.setAttribute('data-camera', camera);
      fallback.alt = camera.replace(/[-_]/g, ' ') + ' camera snapshot';
      face.appendChild(fallback);
      var fallbackCaption = document.createElement('figcaption');
      if (wideDisplay) {
        var fallbackName = document.createElement('span'); fallbackName.className = 'camera-name';
        var fallbackText = document.createElement('span'); fallbackText.className = 'camera-name-text'; fallbackText.textContent = camera.replace(/[-_]/g, ' ');
        fallbackName.appendChild(fallbackText); fallbackName.appendChild(refreshIndicator); fallbackCaption.appendChild(fallbackName);
      } else fallbackCaption.textContent = camera.replace(/[-_]/g, ' ') + ' · snapshot';
      face.appendChild(fallbackCaption);
      return face;
    }
    var video = document.createElement('video');
    video.muted = true;
    video.defaultMuted = true;
    video.setAttribute('muted', '');
    video.autoplay = true;
    video.playsInline = true;
    video.poster = (isKiosk ? '/kiosk/api/essentials' : '/api/essentials') + '/cameras/' + encodeURIComponent(camera) + '/snapshot?refresh=' + Date.now();
    video.setAttribute('aria-label', camera.replace(/[-_]/g, ' ') + ' live camera video');
    var caption = document.createElement('figcaption');
    var nameLabel = document.createElement('span'); nameLabel.className = 'camera-name';
    var nameText = document.createElement('span'); nameText.className = 'camera-name-text'; nameText.textContent = camera.replace(/[-_]/g, ' ');
    nameLabel.appendChild(nameText); caption.appendChild(nameLabel);
    face.appendChild(video); face.appendChild(caption);
    connectCamera(video, camera);
    return face;
  }
  function startCameraProgress() {
    cameraCycleStartedAt = Date.now();
    var completeReported = false;
    if (cameraProgressTimer !== null) window.clearInterval(cameraProgressTimer);
    cameraProgressTimer = window.setInterval(function () {
      var elapsed = Math.min(30000, Date.now() - cameraCycleStartedAt);
      var progress = elapsed / 30000;
      if (progress >= 1 && !completeReported) {
        completeReported = true;
        recordCameraTelemetry('camera_rotation_progress_complete', { duration_ms: elapsed });
      }
      var rails = document.querySelectorAll('#camera-grid .camera-cycle-progress');
      for (var i = 0; i < rails.length; i++) {
        rails[i].style.setProperty('--camera-progress', String(progress));
      }
    }, 100);
  }
  function showCameraGrid(index) {
    var grid = document.getElementById('camera-grid');
    if (!cameraList.length) return;
    cameraIndex = (index + cameraList.length) % cameraList.length;
    stopCameraStreams();
    grid.textContent = '';
    cameraTiles = [];
    for (var slot = 0; slot < Math.min(4, cameraList.length); slot++) {
      var frontIndex = (cameraIndex + slot) % cameraList.length;
      var backStep = cameraList.length > 4 ? 4 : 1;
      var camera = cameraList[frontIndex];
      var nextCamera = cameraList[(frontIndex + backStep) % cameraList.length];
      var tile = document.createElement('figure'); tile.className = 'camera-tile camera-live-tile';
      tile.setAttribute('role', 'button');
      tile.setAttribute('tabindex', '0');
      tile.setAttribute('aria-label', 'Flip to the next camera');
      tile._flipping = false;
      var flipper = document.createElement('div'); flipper.className = 'camera-flipper';
      flipper.appendChild(makeCameraFace('camera-face-front', camera));
      flipper.appendChild(makeCameraFace('camera-face-back', nextCamera));
      (function (cameraTile) {
        cameraTile.addEventListener('click', function (event) {
          var target = event.target;
          if (target && target.closest && target.closest('a,button,input,select,textarea')) return;
          flipCameraTile(cameraTile);
        });
        cameraTile.addEventListener('keydown', function (event) {
          if (event.key === 'Enter' || event.key === ' ') {
            event.preventDefault();
            flipCameraTile(cameraTile);
          }
        });
      })(tile);
      tile.appendChild(flipper);
      var cycleProgress = document.createElement('span');
      cycleProgress.className = 'camera-cycle-progress';
      cycleProgress.setAttribute('aria-hidden', 'true');
      tile.appendChild(cycleProgress);
      grid.appendChild(tile); cameraTiles.push(tile);
      loadCameraSnapshot(tile.querySelector('.camera-face-front .camera-snapshot-live'));
    }
  }
  function prepareNextCameraGroup(generation) {
    if (generation !== cameraSessionGeneration || !wideDisplay || cameraList.length <= 4 || active !== 2) return;
    for (var slot = 0; slot < cameraTiles.length; slot++) {
      var camera = cameraList[(cameraIndex + 4 + slot) % cameraList.length];
      var back = cameraTiles[slot].querySelector('.camera-face-back');
      if (back && !back.querySelector('video')) {
        var prepared = makeCameraFace('camera-face-back', camera);
        back.parentNode.replaceChild(prepared, back);
      }
    }
  }
  function flipCameraGroup(generation) {
    if (generation !== cameraSessionGeneration || active !== 2 || cameraList.length <= 4 || !cameraTiles.length) return;
    var switchStartedAt = Date.now();
    recordCameraTelemetry('camera_rotation_switch_started', {
      progress_elapsed_ms: cameraCycleStartedAt ? switchStartedAt - cameraCycleStartedAt : -1,
      tile_count: cameraTiles.length
    });
    var finished = 0;
    cameraTiles.forEach(function (tile) {
      flipCameraTile(tile, function () {
        finished++;
        if (generation === cameraSessionGeneration && active === 2 && finished === cameraTiles.length) {
          recordCameraTelemetry('camera_rotation_switch_finished', {
            duration_ms: Date.now() - switchStartedAt,
            tile_count: cameraTiles.length
          });
          cameraIndex = (cameraIndex + 4) % cameraList.length;
          startCameraProgress();
          cameraWarmTimer = window.setTimeout(function () { prepareNextCameraGroup(generation); }, 27000);
        }
      });
    });
  }
  function flipCameraTile(tile, onDone) {
    if (!tile || tile._flipping || active !== 2 || cameraList.length <= 1) return;
    var flipper = tile.querySelector('.camera-flipper');
    var front = flipper && flipper.querySelector('.camera-face-front');
    var back = flipper && flipper.querySelector('.camera-face-back');
    if (!flipper || !front || !back || back._cameraIndex < 0) return;
    loadCameraSnapshot(back.querySelector('.camera-snapshot-live'));
    tile._flipping = true;
    tile.classList.add('is-flipping');
    var completed = false;
    var onEnd = function (event) {
      if (completed) return;
      if (event && (event.target !== flipper || event.propertyName !== 'transform')) return;
      completed = true;
      flipper.removeEventListener('transitionend', onEnd);
      if (front && back) {
        stopCameraPlayer(front.querySelector('video'));
        front.className = 'camera-face camera-face-back';
        back.className = 'camera-face camera-face-front';
        clearCameraFace(front);
        var step = cameraList.length > 4 ? 4 : 1;
        var nextIndex = (back._cameraIndex + step) % cameraList.length;
        var prepared = makeCameraFace('camera-face-back', cameraList[nextIndex]);
        front.parentNode.replaceChild(prepared, front);
      }
      tile.classList.add('is-resetting');
      tile.classList.remove('is-flipping');
      void flipper.offsetWidth;
      window.requestAnimationFrame(function () {
        tile.classList.remove('is-resetting');
        tile._flipping = false;
        if (onDone) onDone();
      });
    };
    flipper.addEventListener('transitionend', onEnd);
    window.setTimeout(function () { onEnd(); }, 850);
  }
  function refreshCameraOverview() {
    var refreshGeneration = ++cameraRefreshGeneration;
    var essentialsBase = isKiosk ? '/kiosk/api/essentials' : '/api/essentials';
    requestJSON(essentialsBase + '/cameras', function (error, data) {
      if (refreshGeneration !== cameraRefreshGeneration || active !== 2) return;
      var grid = document.getElementById('camera-grid');
      stopCameraRotation();
      stopCameraStreams();
      if (error) { cameraList = []; grid.textContent = 'Camera views are unavailable.'; return; }
      var cameras = data.cameras || [];
      cameraList = cameras;
      grid.textContent = '';
      grid.setAttribute('data-visible-cameras', String(Math.min(4, cameras.length)));
      if (!cameras.length) {
        var none = document.createElement('p'); none.className = 'camera-view-message'; none.textContent = 'No cameras are configured.'; grid.appendChild(none); return;
      }
      if (wideDisplay) {
        grid.setAttribute('data-live-cameras', 'true');
        showCameraGrid(cameraIndex);
        startCameraProgress();
        cameraSnapshotTimer = window.setInterval(refreshCameraSnapshots, 2000);
        if (cameraList.length > 4) {
          grid.setAttribute('data-camera-cycle', 'true');
          var generation = cameraSessionGeneration;
          cameraWarmTimer = window.setTimeout(function () { prepareNextCameraGroup(generation); }, 27000);
          cameraCycleTimer = window.setInterval(function () { flipCameraGroup(generation); }, 30000);
        } else {
          grid.removeAttribute('data-camera-cycle');
        }
        return;
      }
      grid.removeAttribute('data-live-cameras');
      cameras.forEach(function (camera) {
        var figure = document.createElement('figure'); figure.className = 'camera-tile';
        var image = document.createElement('img');
        image.setAttribute('data-camera', camera);
        image.alt = camera.replace(/[-_]/g, ' ') + ' camera snapshot';
        image.loading = 'lazy';
        var caption = document.createElement('figcaption'); caption.textContent = camera.replace(/[-_]/g, ' ');
        figure.appendChild(image); figure.appendChild(caption); grid.appendChild(figure);
        loadCameraSnapshot(image);
      });
    });
  }
  function refreshHint() {
    var remaining = Math.max(0, Math.ceil((holdUntil-Date.now())/1000));
    if (!rotate.checked) hint.textContent = 'Auto-switch off · swipe or tap to change';
    else if (!drawer.hidden) hint.textContent = 'Settings open · auto-switch paused';
    else if (remaining > 0) hint.textContent = 'Your choice held for ' + (remaining > 60 ? Math.ceil(remaining / 60) + ' min' : remaining + ' sec');
    else if (panel.contains(document.activeElement) && document.activeElement !== body) hint.textContent = 'Finish interacting to resume auto-switch';
    else if (active === 2 && wideDisplay && cameraList.length > 4) hint.textContent = 'Camera groups rotate every 30s';
    else hint.textContent = 'Auto-switches every 30s · swipe to explore';
  }
  function show(index, manual) {
    var previous = active;
    active = (index + panes.length) % panes.length;
    var switchAt = Date.now();
    var completedAt = rotationProgressCompletedAt;
    recordCameraTelemetry('feature_panel_switch', {
      from_panel: ['weather','tasks','cameras'][previous],
      to_panel: ['weather','tasks','cameras'][active],
      trigger: manual ? 'manual' : 'automatic',
      progress_to_switch_ms: completedAt ? Math.max(0, switchAt - completedAt) : -1
    });
    rotationProgressCompletedAt = 0;
    cameraPanelShownAt = active === 2 ? switchAt : 0;
    document.getElementById('feature-panel').classList.toggle('cameras-focus',wideDisplay && active===2);
    document.querySelector('.dashboard').classList.toggle('cameras-selected',wideDisplay && active===2);
    if (active !== 2) { cameraRefreshGeneration++; stopCameraRotation(); stopCameraStreams(); }
    for (var i=0;i<panes.length;i++) {
      var visible=i===active;
      panes[i].hidden=!visible;
      panes[i].className=paneBaseClasses[i]+(visible?' is-active':'');
      tabs[i].setAttribute('aria-selected',String(visible));
      tabs[i].setAttribute('tabindex',visible?'0':'-1');
      tabs[i].className='feature-tab'+(visible?' is-active':'');
      if (pager[i]) pager[i].className='pager-dot'+(visible?' active':'');
    }
    name.textContent=['Outside today','Family tasks','Cameras'][active];
    if (active === 2) refreshCameraOverview();
    if (manual) pause();
  }
  for (var i=0;i<tabs.length;i++) (function (n) {
    tabs[n].addEventListener('click',function () { show(n,true); });
    tabs[n].addEventListener('keydown',function (event) { if(event.key==='ArrowRight'||event.key==='ArrowLeft'){event.preventDefault();show(n+(event.key==='ArrowRight'?1:-1),true);tabs[active].focus();} });
  })(i);
  panes[2].addEventListener('animationend', function (event) {
    if (event.target !== panes[2] || event.animationName !== 'panel-arrive' || !cameraPanelShownAt) return;
    recordCameraTelemetry('camera_panel_visible', { duration_ms: Date.now() - cameraPanelShownAt });
    cameraPanelShownAt = 0;
  });
  document.getElementById('feature-prev').addEventListener('click', function () { show(active-1,true); });
  document.getElementById('feature-next').addEventListener('click', function () { show(active+1,true); });
  var startX=0,startY=0,tracking=false;
  panel.addEventListener('touchstart',function (event) { if(event.touches.length!==1)return; startX=event.touches[0].clientX;startY=event.touches[0].clientY;tracking=true; },{passive:true});
  panel.addEventListener('touchend',function (event) {
    if(!tracking || !event.changedTouches.length)return; tracking=false;
    var dx=event.changedTouches[0].clientX-startX,dy=event.changedTouches[0].clientY-startY;
    if(Math.abs(dx)>60 && Math.abs(dx)>Math.abs(dy)*1.4) show(active+(dx<0?1:-1),true);
    else if(Math.abs(dx)>4 || Math.abs(dy)>4) pause();
  },{passive:true});
  /* Do not yank a form away while the user is typing or checking tasks. */
  panel.addEventListener('focusin',function () { pause(); });
  panel.addEventListener('click',function () { pause(); });
  window.setInterval(function () {
    var now = Date.now();
    var blocked = !rotate.checked || now < holdUntil || document.hidden || !drawer.hidden || document.getElementById('feature-panel').classList.contains('sensor-alert-active');
    if (!blocked && panel.contains(document.activeElement) && document.activeElement !== body) blocked = true;
    if (blocked) { resetRotationProgress(); return; }
    if (!rotationStartedAt) rotationStartedAt = now;
    var elapsed = now - rotationStartedAt;
    var duration = panelRotationDuration();
    var progress = Math.min(1, elapsed / duration);
    var secondsLeft = Math.max(0, Math.ceil((duration - elapsed) / 1000));
    rotationProgress.hidden = false;
    rotationProgress.style.transform = 'scaleX(' + progress + ')';
    rotationProgress.setAttribute('aria-valuemax', String(Math.ceil(duration / 1000)));
    rotationProgress.setAttribute('aria-valuenow', String(Math.floor(progress * duration / 1000)));
    rotationProgress.setAttribute('aria-valuetext', secondsLeft + ' seconds until next panel');
    if (elapsed >= duration) {
      recordCameraTelemetry('feature_rotation_progress_complete', { duration_ms: elapsed });
      rotationProgressCompletedAt = Date.now();
      show(active + 1, false);
      rotationStartedAt = Date.now();
      rotationProgress.style.transform = 'scaleX(0)';
      rotationProgress.setAttribute('aria-valuenow', '0');
      rotationProgress.setAttribute('aria-valuetext', '30 seconds until next panel');
    }
  },100);
  window.setInterval(refreshHint,1000);
  /* Retain the currently selected tasks pane after a task form POST / redirect. */
  panel.addEventListener('submit',function (event) {
    var target = event.target;
    if (!target || target.tagName !== 'FORM') return;
    var node = target;
    while (node && node !== panel && node.id !== 'pane-tasks') node = node.parentNode;
    if (node && node.id === 'pane-tasks') save('resume-tasks',String(Date.now()));
  });
  var resumed = Number(read('resume-tasks', '0'));
  if (resumed && Date.now()-resumed < 15000) { show(1,true); save('resume-tasks','0'); }

  function closeSettings() {
    drawer.classList.remove('is-open');backdrop.classList.remove('is-open');launch.setAttribute('aria-expanded','false');
    window.clearTimeout(settingsCloseTimer);
    settingsCloseTimer=window.setTimeout(function () {
      if (!drawer.classList.contains('is-open')) { drawer.hidden=true;backdrop.hidden=true; }
    },260);
    if (lastFocus && lastFocus.focus) lastFocus.focus();
    pause();
  }
  launch.addEventListener('click',function () {
    window.clearTimeout(settingsCloseTimer);
    lastFocus=document.activeElement;drawer.hidden=false;backdrop.hidden=false;launch.setAttribute('aria-expanded','true');
    window.requestAnimationFrame(function () { drawer.classList.add('is-open');backdrop.classList.add('is-open'); });
    theme.focus();pause();
  });
  drawer.addEventListener('transitionend',function (event) {
    if (event.target===drawer&&!drawer.classList.contains('is-open')) {
      window.clearTimeout(settingsCloseTimer);drawer.hidden=true;backdrop.hidden=true;
    }
  });
  document.getElementById('close-settings').addEventListener('click',closeSettings);
  backdrop.addEventListener('click',closeSettings);
  document.addEventListener('keydown',function (event) {
    if (event.key==='Escape'&&!drawer.hidden) closeSettings();
    if(event.key==='Tab'&&!drawer.hidden) {
      var choices=drawer.querySelectorAll('button,select,input'); var first=choices[0],last=choices[choices.length-1];
      if(event.shiftKey&&document.activeElement===first){event.preventDefault();last.focus();}
      else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();first.focus();}
    }
  });
  var doorAlert = document.getElementById('door-alert');
  var sensorPane = document.getElementById('pane-sensor-alert');
  var sensorRefreshTimer = null;
  var sensorImageURL = '';
  var dismissedSensors = {};
  function clearSensorAlert() {
    if (!sensorPane) return;
    document.getElementById('feature-panel').classList.remove('sensor-alert-active');
    window.clearInterval(sensorRefreshTimer);
    sensorRefreshTimer = null;
    sensorImageURL = '';
    sensorPane.hidden = true;
    document.querySelector('.feature-tabs').hidden = false;
    document.getElementById('feature-prev').hidden = false;
    document.getElementById('feature-next').hidden = false;
    show(active, false);
  }
  function showSensorAlert(event) {
    var key = (event.kind || '') + ':' + (event.sensor || '');
    if (!event.active) { delete dismissedSensors[key]; clearSensorAlert(); return; }
    if (dismissedSensors[key]) return;
    cameraRefreshGeneration++;
    stopCameraRotation();
    stopCameraStreams();
    document.getElementById('feature-panel').classList.add('sensor-alert-active');
    document.querySelector('.feature-tabs').hidden = true;
    document.getElementById('feature-prev').hidden = true;
    document.getElementById('feature-next').hidden = true;
    panes.forEach(function (pane) { pane.hidden = true; });
    sensorPane.hidden = false;
    name.textContent = event.kind === 'water' ? 'Water detected' : 'Freezer temperature';
    document.getElementById('sensor-alert-kicker').textContent = event.severity === 'test' ? 'DASHBOARD · TEST PREVIEW' : event.kind === 'water' ? 'WATER SENSOR · ALERT' : 'FREEZER · ' + (event.severity || 'ALERT').toUpperCase();
    document.getElementById('sensor-alert-title').textContent = event.kind === 'water' ? 'Water detected in the basement' : 'Freezer temperature alert';
    document.getElementById('sensor-alert-summary').textContent = event.severity === 'test' ? 'Preview only · no sensor reading or ntfy alert was changed.' : event.kind === 'water' ? (event.sensor || 'Water sensor') + ' is detecting water.' : (event.sensor || 'Freezer') + ' is out of range.';
    document.getElementById('sensor-alert-reading').textContent = event.kind === 'temperature' && typeof event.temperature_f === 'number' ? event.temperature_f.toFixed(1) + ' °F' : 'WATER';
    sensorImageURL = event.snapshot_url || '';
    if (event.snapshot) document.getElementById('sensor-alert-image').src = event.snapshot;
    else if (sensorImageURL) document.getElementById('sensor-alert-image').src = sensorImageURL + (sensorImageURL.indexOf('?') < 0 ? '?' : '&') + 'refresh=' + Date.now();
    window.clearInterval(sensorRefreshTimer);
    if (sensorImageURL) sensorRefreshTimer = window.setInterval(function () { document.getElementById('sensor-alert-image').src = sensorImageURL + (sensorImageURL.indexOf('?') < 0 ? '?' : '&') + 'refresh=' + Date.now(); }, 30000);
    refreshHint();
  }
  document.getElementById('sensor-alert-dismiss').addEventListener('click', function () {
    var eventKey = sensorPane.getAttribute('data-sensor-key');
    if (eventKey) dismissedSensors[eventKey] = true;
    clearSensorAlert();
  });
  if (doorAlert && window.EventSource) {
    var seenDoorEvents = {};
    var doorAlertDuration = 30000;
    var doorAlertDeadline = 0;
    var doorAlertTimer = null;
    var doorAlertProgressTimer = null;
    var doorAlertHistoryEntry = false;
    var doorAlertClose = document.getElementById('door-alert-close');
    var doorAlertImage = document.getElementById('door-alert-snapshot');
    var doorAlertClip = document.getElementById('door-alert-clip');
    var doorAlertProgress = document.getElementById('door-alert-progress');
    var doorAlertRemaining = document.getElementById('door-alert-remaining');

    function dismissDoorAlert(fromHistory) {
      if (doorAlert.hidden) return;
      doorAlert.hidden = true;
      doorAlertImage.removeAttribute('src');
      window.clearTimeout(doorAlertTimer);
      window.clearInterval(doorAlertProgressTimer);
      doorAlertTimer = null;
      doorAlertProgressTimer = null;
      if (!fromHistory && doorAlertHistoryEntry) {
        doorAlertHistoryEntry = false;
        window.history.back();
      }
    }

    function updateDoorAlertProgress() {
      var remaining = Math.max(0, doorAlertDeadline - Date.now());
      var seconds = Math.ceil(remaining / 1000);
      doorAlertProgress.setAttribute('aria-valuenow', String(seconds));
      doorAlertProgress.firstElementChild.style.width = (remaining / doorAlertDuration * 100) + '%';
      doorAlertRemaining.textContent = 'Closing in ' + seconds + (seconds === 1 ? ' second' : ' seconds');
      if (remaining <= 0) dismissDoorAlert(false);
    }

    var doorStream = new EventSource(isKiosk ? '/kiosk/frigate/events/stream' : '/api/frigate/events/stream');
    doorStream.addEventListener('frigate', function (message) {
      try {
        var event = JSON.parse(message.data);
        if (event.kind === 'dashboard-refresh') {
          scheduleSharedRefresh();
          return;
        }
        if (event.kind === 'temperature' || event.kind === 'water') {
          updateHeaderSensorFromEvent(event);
          sensorPane.setAttribute('data-sensor-key', event.kind + ':' + (event.sensor || ''));
          showSensorAlert(event);
          return;
        }
        if (seenDoorEvents[event.id]) return;
        seenDoorEvents[event.id] = true;
        document.getElementById('door-alert-camera').textContent = (event.camera || 'Camera').replace(/[-_]/g, ' ');
        doorAlertImage.src = event.snapshot || '';
        doorAlertClip.href = event.clip_url || '#';
        if (doorAlert.hidden) {
          doorAlert.hidden = false;
          try {
            window.history.pushState({ doorAlert: true }, '', window.location.href);
            doorAlertHistoryEntry = true;
          } catch (_) {}
        }
        window.clearTimeout(doorAlertTimer);
        window.clearInterval(doorAlertProgressTimer);
        doorAlertDeadline = Date.now() + doorAlertDuration;
        updateDoorAlertProgress();
        doorAlertProgressTimer = window.setInterval(updateDoorAlertProgress, 200);
        doorAlertTimer = window.setTimeout(function () { dismissDoorAlert(false); }, doorAlertDuration);
        doorAlertClose.focus({ preventScroll: true });
      } catch (_) {}
    });
    doorAlertClose.addEventListener('click', function () { dismissDoorAlert(false); });
    window.addEventListener('popstate', function () {
      if (!doorAlert.hidden) {
        doorAlertHistoryEntry = false;
        dismissDoorAlert(true);
      }
    });
    document.addEventListener('keydown', function (event) {
      if (!doorAlert.hidden && ['Escape', 'Backspace', 'BrowserBack', 'GoBack', 'Back'].includes(event.key)) {
        event.preventDefault();
        event.stopPropagation();
        dismissDoorAlert(false);
      }
    });
  }
  refreshHint();
  if ('serviceWorker' in navigator && window.isSecureContext) {
    navigator.serviceWorker.register('/sw.js').catch(function () {
      /* The dashboard remains usable when the browser blocks service workers. */
    });
  }
})();
