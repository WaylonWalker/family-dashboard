/* Minimal ES2017-free controller for Android 6 / older WebView kiosks. */
(function () {
  'use strict';
  var root = document.documentElement;
  var body = document.body;
  var panel = document.getElementById('feature-window');
  var panes = [document.getElementById('pane-weather'), document.getElementById('pane-tasks'), document.getElementById('pane-sensors')];
  var tabs = [document.getElementById('tab-weather'), document.getElementById('tab-tasks'), document.getElementById('tab-sensors')];
  var paneBaseClasses = ['feature-pane','feature-pane','feature-pane sensors-overview'];
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
  var cameraList = [];
  var cameraIndex = 0;
  var sensorRefreshGeneration = 0;
  var wakeLockSentinel = null;
  var wakeLockRequestId = 0;
  var wakeLockPending = false;
  var wakeLockRetryTimer = null;
  var active = 0;
  var holdUntil = 0;
  var rotationStartedAt = 0;
  var lastFocus = null;

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
  function sensorLabel(reading) {
    if (reading.kind === 'temperature') return 'Freezer';
    if (reading.kind === 'water') return 'Basement water';
    return (reading.name || 'Sensor').replace(/[-_]/g, ' ');
  }
  function updateHeaderSensors(error, readings) {
    var freezer = document.getElementById('header-freezer');
    var basement = document.getElementById('header-basement');
    var freezerValue = freezer.querySelector('strong');
    var basementValue = basement.querySelector('strong');
    var freezerSignal = freezer.querySelector('.sensor-signal');
    var basementSignal = basement.querySelector('.sensor-signal');
    function setConnection(node, signal, reading) {
      var online = false;
      if (reading && typeof reading.online === 'boolean') online = reading.online;
      else if (reading && reading.updated_at) {
        var lastSeen = new Date(reading.updated_at).getTime();
        online = !isNaN(lastSeen) && Date.now() - lastSeen < 30 * 60 * 1000;
      }
      node.classList.toggle('is-online', online);
      node.classList.toggle('is-offline', !online);
      signal.setAttribute('aria-label', online ? 'Online' : 'Offline');
      node.title = node.textContent.trim() + ' sensor ' + (online ? 'online' : 'offline');
      return online;
    }
    freezer.classList.remove('is-alert');
    basement.classList.remove('is-alert');
    if (error) {
      freezerValue.textContent = 'Offline';
      basementValue.textContent = 'Offline';
      setConnection(freezer, freezerSignal, null);
      setConnection(basement, basementSignal, null);
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
    freezerValue.textContent = temperature && freezerOnline && typeof temperature.temperature_f === 'number' ? temperature.temperature_f.toFixed(1) + '°F' : 'Offline';
    basementValue.textContent = water && basementOnline ? (water.detected ? 'Wet' : 'Dry') : 'Offline';
    if (water && water.detected) basement.classList.add('is-alert');
  }
  function refreshHeaderSensors() {
    var essentialsBase = isKiosk ? '/kiosk/api/essentials' : '/api/essentials';
    requestJSON(essentialsBase + '/sensors', function (error, data) {
      updateHeaderSensors(error, data && data.readings);
    });
  }
  function renderSensorReadings(readings) {
    var list = document.getElementById('sensor-reading-list');
    list.textContent = '';
    readings = readings.filter(function (reading) {
      if (reading.kind !== 'device') return true;
      var device = (reading.name || '').toLowerCase().replace(/[^a-z0-9]/g, '');
      return device !== 'standupfreezer' && device !== 'doorbell';
    });
    if (!readings.length) {
      var empty = document.createElement('p');
      empty.className = 'sensor-view-message';
      empty.textContent = 'No sensor readings have arrived yet.';
      list.appendChild(empty);
      return;
    }
    readings.forEach(function (reading) {
      var card = document.createElement('article');
      card.className = 'sensor-reading-card' + ((reading.kind === 'water' && reading.detected) || (reading.kind === 'device' && reading.battery_low) ? ' is-alert' : '');
      var label = document.createElement('span');
      label.className = 'sensor-reading-label';
      label.textContent = sensorLabel(reading);
      var value = document.createElement('strong');
      value.className = 'sensor-reading-value';
      var detail = document.createElement('small');
      if (reading.kind === 'temperature') {
        value.textContent = Number(reading.temperature_f).toFixed(1) + ' °F';
        detail.textContent = [reading.humidity == null ? '' : 'Humidity ' + Math.round(reading.humidity) + '%', reading.battery == null ? '' : 'Battery ' + Math.round(reading.battery) + '%'].filter(Boolean).join(' · ') || 'Freezer temperature';
      } else if (reading.kind === 'water') {
        value.textContent = reading.detected ? 'WATER DETECTED' : 'Dry';
        detail.textContent = [reading.battery == null ? '' : 'Battery ' + Math.round(reading.battery) + '%', reading.linkquality == null ? '' : 'Signal ' + reading.linkquality].filter(Boolean).join(' · ') || 'Basement leak sensor';
      } else {
        value.textContent = reading.battery == null ? 'Online' : Math.round(reading.battery) + '%';
        detail.textContent = [reading.battery_low ? 'Low battery' : '', reading.linkquality == null ? '' : 'Signal ' + reading.linkquality].filter(Boolean).join(' · ') || 'Battery';
      }
      if (reading.updated_at) detail.textContent += ' · seen ' + new Date(reading.updated_at).toLocaleTimeString([], {hour:'numeric',minute:'2-digit'});
      card.appendChild(label); card.appendChild(value); card.appendChild(detail); list.appendChild(card);
    });
  }
  function stopCameraRotation() {
    if (cameraCycleTimer !== null) window.clearInterval(cameraCycleTimer);
    cameraCycleTimer = null;
  }
  function showCamera(index) {
    var grid = document.getElementById('camera-grid');
    if (!cameraList.length) return;
    cameraIndex = (index + cameraList.length) % cameraList.length;
    var camera = cameraList[cameraIndex];
    var label = camera.replace(/[-_]/g, ' ');
    var figure = document.createElement('figure'); figure.className = 'camera-tile';
    var image = document.createElement('img');
    image.src = (isKiosk ? '/kiosk/api/essentials' : '/api/essentials') + '/cameras/' + encodeURIComponent(camera) + '/snapshot?refresh=' + Date.now();
    image.alt = label + ' camera snapshot';
    var caption = document.createElement('figcaption'); caption.textContent = label;
    var position = document.createElement('span'); position.className = 'camera-position';
    var progress = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    progress.setAttribute('class', 'camera-position-progress'); progress.setAttribute('viewBox', '0 0 44 44'); progress.setAttribute('aria-hidden', 'true');
    var track = document.createElementNS('http://www.w3.org/2000/svg', 'circle'); track.setAttribute('class', 'camera-position-track'); track.setAttribute('cx', '22'); track.setAttribute('cy', '22'); track.setAttribute('r', '19');
    var arc = document.createElementNS('http://www.w3.org/2000/svg', 'circle'); arc.setAttribute('class', 'camera-position-arc'); arc.setAttribute('cx', '22'); arc.setAttribute('cy', '22'); arc.setAttribute('r', '19');
    progress.appendChild(track); progress.appendChild(arc);
    var count = document.createElement('span'); count.textContent = (cameraIndex + 1) + ' / ' + cameraList.length;
    position.appendChild(progress); position.appendChild(count);
    figure.appendChild(image); figure.appendChild(caption); figure.appendChild(position);
    grid.textContent = '';
    grid.appendChild(figure);
  }
  function refreshSensorOverview() {
    var refreshGeneration = ++sensorRefreshGeneration;
    var essentialsBase = isKiosk ? '/kiosk/api/essentials' : '/api/essentials';
    requestJSON(essentialsBase + '/sensors', function (error, data) {
      if (refreshGeneration !== sensorRefreshGeneration || active !== 2) return;
      var updated = document.getElementById('sensors-updated');
      if (error) {
        updated.textContent = 'Sensor service unavailable';
        var list = document.getElementById('sensor-reading-list');
        list.textContent = '';
        var unavailable = document.createElement('p'); unavailable.className = 'sensor-view-message'; unavailable.textContent = 'Sensor readings are unavailable.'; list.appendChild(unavailable);
        return;
      }
      renderSensorReadings(data.readings || []);
      updated.textContent = 'Updated ' + new Date(data.updated_at).toLocaleTimeString([], {hour:'numeric',minute:'2-digit'});
    });
    requestJSON(essentialsBase + '/cameras', function (error, data) {
      if (refreshGeneration !== sensorRefreshGeneration || active !== 2) return;
      var grid = document.getElementById('camera-grid');
      stopCameraRotation();
      if (error) { cameraList = []; grid.textContent = 'Camera views are unavailable.'; return; }
      var cameras = data.cameras || [];
      cameraList = cameras;
      grid.textContent = '';
      if (!cameras.length) {
        var none = document.createElement('p'); none.className = 'sensor-view-message'; none.textContent = 'No cameras are configured in Frigate.'; grid.appendChild(none); return;
      }
      if (wideDisplay) {
        showCamera(cameraIndex);
        if (cameras.length > 1) cameraCycleTimer = window.setInterval(function () { showCamera(cameraIndex + 1); }, 30000);
        return;
      }
      cameras.forEach(function (camera) {
        var figure = document.createElement('figure'); figure.className = 'camera-tile';
        var image = document.createElement('img');
        image.src = essentialsBase + '/cameras/' + encodeURIComponent(camera) + '/snapshot?refresh=' + Date.now();
        image.alt = camera.replace(/[-_]/g, ' ') + ' camera snapshot';
        image.loading = 'lazy';
        var caption = document.createElement('figcaption'); caption.textContent = camera.replace(/[-_]/g, ' ');
        figure.appendChild(image); figure.appendChild(caption); grid.appendChild(figure);
      });
    });
  }
  function refreshHint() {
    var remaining = Math.max(0, Math.ceil((holdUntil-Date.now())/1000));
    if (!rotate.checked) hint.textContent = 'Auto-switch off · swipe or tap to change';
    else if (!drawer.hidden) hint.textContent = 'Settings open · auto-switch paused';
    else if (remaining > 0) hint.textContent = 'Your choice held for ' + (remaining > 60 ? Math.ceil(remaining / 60) + ' min' : remaining + ' sec');
    else if (panel.contains(document.activeElement) && document.activeElement !== body) hint.textContent = 'Finish interacting to resume auto-switch';
    else hint.textContent = 'Auto-switches every 30s · swipe to explore';
  }
  function show(index, manual) {
    active = (index + panes.length) % panes.length;
    document.getElementById('feature-panel').classList.toggle('sensors-focus',wideDisplay && active===2);
    document.querySelector('.dashboard').classList.toggle('sensors-selected',wideDisplay && active===2);
    if (active !== 2) { sensorRefreshGeneration++; stopCameraRotation(); }
    for (var i=0;i<panes.length;i++) {
      var visible=i===active;
      panes[i].hidden=!visible;
      panes[i].className=paneBaseClasses[i]+(visible?' is-active':'');
      tabs[i].setAttribute('aria-selected',String(visible));
      tabs[i].setAttribute('tabindex',visible?'0':'-1');
      tabs[i].className='feature-tab'+(visible?' is-active':'');
      if (pager[i]) pager[i].className='pager-dot'+(visible?' active':'');
    }
    name.textContent=['Outside today','Family tasks','Sensors'][active];
    if (active === 2) refreshSensorOverview();
    if (manual) pause();
  }
  for (var i=0;i<tabs.length;i++) (function (n) {
    tabs[n].addEventListener('click',function () { show(n,true); });
    tabs[n].addEventListener('keydown',function (event) { if(event.key==='ArrowRight'||event.key==='ArrowLeft'){event.preventDefault();show(n+(event.key==='ArrowRight'?1:-1),true);tabs[active].focus();} });
  })(i);
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
    var blocked = !rotate.checked || now < holdUntil || document.hidden || !drawer.hidden;
    if (!blocked && panel.contains(document.activeElement) && document.activeElement !== body) blocked = true;
    if (blocked) { resetRotationProgress(); return; }
    if (!rotationStartedAt) rotationStartedAt = now;
    var elapsed = now - rotationStartedAt;
    var progress = Math.min(1, elapsed / 30000);
    var secondsLeft = Math.max(0, Math.ceil((30000 - elapsed) / 1000));
    rotationProgress.hidden = false;
    rotationProgress.style.transform = 'scaleX(' + progress + ')';
    rotationProgress.setAttribute('aria-valuenow', String(Math.floor(progress * 30)));
    rotationProgress.setAttribute('aria-valuetext', secondsLeft + ' seconds until next panel');
    if (elapsed >= 30000) {
      show(active + 1, false);
      rotationStartedAt = Date.now();
      rotationProgress.style.transform = 'scaleX(0)';
      rotationProgress.setAttribute('aria-valuenow', '0');
      rotationProgress.setAttribute('aria-valuetext', '30 seconds until next panel');
    }
  },100);
  window.setInterval(refreshHint,1000);
  /* Retain the currently selected tasks pane after a task form POST / redirect. */
  var submitted = document.querySelectorAll('#pane-tasks form');
  for (var n=0;n<submitted.length;n++) submitted[n].addEventListener('submit',function () { save('resume-tasks',String(Date.now())); });
  var resumed = Number(read('resume-tasks', '0'));
  if (resumed && Date.now()-resumed < 15000) { show(1,true); save('resume-tasks','0'); }

  function closeSettings() {
    drawer.hidden=true;backdrop.hidden=true;launch.setAttribute('aria-expanded','false');
    if (lastFocus && lastFocus.focus) lastFocus.focus();
    pause();
  }
  launch.addEventListener('click',function () {
    lastFocus=document.activeElement;drawer.hidden=false;backdrop.hidden=false;launch.setAttribute('aria-expanded','true');
    theme.focus();pause();
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
        if (event.kind === 'temperature' || event.kind === 'water') {
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
