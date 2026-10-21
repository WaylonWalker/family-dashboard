/* Minimal ES2017-free controller for Android 6 / older WebView kiosks. */
(function () {
  'use strict';
  var root = document.documentElement;
  var body = document.body;
  var panel = document.getElementById('feature-window');
  var panes = [document.getElementById('pane-weather'), document.getElementById('pane-tasks')];
  var tabs = [document.getElementById('tab-weather'), document.getElementById('tab-tasks')];
  var pager = document.querySelectorAll('.pager-dot');
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
  var active = 0;
  var holdUntil = 0;
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
    theme.value='auto'; texture.value='paper'; font.value='system'; style.value='balanced'; rotate.checked=true;
    save('theme','auto');save('texture','paper');save('font','system');save('style','balanced');save('rotate','true');applyTheme();refreshHint();
  });
  applyTheme();
  function updateClock() {
    try {
      var time = new Intl.DateTimeFormat('en-US',{timeZone:'America/Chicago',hour:'numeric',minute:'2-digit'}).format(new Date());
      var date = new Intl.DateTimeFormat('en-US',{timeZone:'America/Chicago',weekday:'long',month:'long',day:'numeric'}).format(new Date());
      document.getElementById('live-clock').textContent=time;
      document.getElementById('live-date').textContent=date;
    } catch(e) { /* Keep server-rendered local clock on unsupported devices. */ }
  }
  updateClock();
  window.setInterval(function () { if (theme.value === 'auto') applyTheme(); updateClock(); }, 60000);

  function pause() { holdUntil = Date.now() + 120000; refreshHint(); }
  function refreshHint() {
    var remaining = Math.max(0, Math.ceil((holdUntil-Date.now())/1000));
    if (!rotate.checked) hint.textContent = 'Auto-switch off · swipe or tap to change';
    else if (remaining > 0) hint.textContent = 'Your choice held for ' + (remaining > 60 ? Math.ceil(remaining / 60) + ' min' : remaining + ' sec');
    else hint.textContent = 'Auto-switches every 30s · swipe to explore';
  }
  function show(index, manual) {
    active = (index + panes.length) % panes.length;
    for (var i=0;i<panes.length;i++) {
      var visible=i===active;
      panes[i].hidden=!visible;
      panes[i].className='feature-pane'+(visible?' is-active':'');
      tabs[i].setAttribute('aria-selected',String(visible));
      tabs[i].setAttribute('tabindex',visible?'0':'-1');
      tabs[i].className='feature-tab'+(visible?' is-active':'');
      if (pager[i]) pager[i].className='pager-dot'+(visible?' active':'');
    }
    name.textContent=active===0?'Outside today':'Family tasks';
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
    refreshHint();
    if (!rotate.checked || Date.now()<holdUntil || document.hidden || !drawer.hidden) return;
    if (panel.contains(document.activeElement) && document.activeElement !== body) return;
    show(active+1,false);
  },30000);
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
  refreshHint();
})();
