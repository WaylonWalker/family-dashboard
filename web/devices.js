/* Paired display management and appearance controls. */
(function () {
  'use strict';
  var root = document.documentElement;
  var cards = Array.prototype.slice.call(document.querySelectorAll('.device-card'));
  var search = document.getElementById('device-search');
  var noResults = document.getElementById('no-results');
  var filter = 'all';

  function updateDevices() {
    if (!cards.length) return;
    var query = search ? search.value.trim().toLowerCase() : '';
    var visible = 0;
    cards.forEach(function (card) {
      var state = card.getAttribute('data-state');
      var inactive = state === 'revoked' || state === 'expired';
      var stateMatches = filter === 'all' || (filter === 'active' && state === 'active') || (filter === 'inactive' && inactive);
      var queryMatches = (card.getAttribute('data-search') || '').toLowerCase().indexOf(query) !== -1;
      card.hidden = !(stateMatches && queryMatches);
      if (!card.hidden) visible += 1;
    });
    if (noResults) noResults.hidden = visible !== 0;
  }

  Array.prototype.forEach.call(document.querySelectorAll('[data-filter]'), function (button) {
    button.addEventListener('click', function () {
      filter = button.getAttribute('data-filter') || 'all';
      Array.prototype.forEach.call(document.querySelectorAll('[data-filter]'), function (item) {
        item.setAttribute('aria-pressed', item === button ? 'true' : 'false');
      });
      updateDevices();
    });
  });
  if (search) search.addEventListener('input', updateDevices);

  var panel = document.getElementById('appearance-panel');
  var backdrop = document.getElementById('appearance-backdrop');
  var openButton = document.getElementById('open-appearance');
  var closeButton = document.getElementById('close-appearance');
  var theme = document.getElementById('theme-select');
  var texture = document.getElementById('texture-select');
  var style = document.getElementById('style-select');
  var font = document.getElementById('font-select');
  var storageAvailable = true;

  function read(key, fallback) {
    try { return window.localStorage.getItem('familyhq.' + key) || fallback; }
    catch (error) { storageAvailable = false; return fallback; }
  }
  function save(key, value) {
    try { window.localStorage.setItem('familyhq.' + key, value); }
    catch (error) { storageAvailable = false; }
  }
  function choose(select, value, fallback) {
    var found = false;
    Array.prototype.forEach.call(select.options, function (option) { if (option.value === value) found = true; });
    if (!found && value) {
      var option = document.createElement('option');
      option.value = value;
      option.textContent = 'Saved palette · ' + value.replace(/-/g, ' ');
      select.appendChild(option);
    }
    select.value = found || value ? value : fallback;
  }
  function applyAppearance() {
    if (theme.value !== 'auto') root.setAttribute('data-theme', theme.value);
    root.setAttribute('data-texture', texture.value);
    root.setAttribute('data-style', style.value);
    root.setAttribute('data-font', font.value);
    var color = window.getComputedStyle(root).getPropertyValue('--bg').trim();
    var meta = document.querySelector('meta[name="theme-color"]');
    if (meta && color) meta.setAttribute('content', color);
  }

  if (panel && backdrop && openButton && closeButton && theme && texture && style && font) {
    choose(theme, read('theme', 'auto'), 'auto');
    texture.value = read('texture', 'paper');
    style.value = read('style', 'balanced');
    font.value = read('font', 'system');
    applyAppearance();

    function openPanel() {
      panel.hidden = false;
      backdrop.hidden = false;
      openButton.setAttribute('aria-expanded', 'true');
      closeButton.focus();
    }
    function closePanel() {
      panel.hidden = true;
      backdrop.hidden = true;
      openButton.setAttribute('aria-expanded', 'false');
      openButton.focus();
    }
    openButton.addEventListener('click', openPanel);
    closeButton.addEventListener('click', closePanel);
    backdrop.addEventListener('click', closePanel);
    panel.addEventListener('keydown', function (event) {
      if (event.key === 'Escape') {
        event.preventDefault();
        closePanel();
      }
      if (event.key === 'Tab') {
        var controls = panel.querySelectorAll('button,select,a[href]');
        if (!controls.length) return;
        var first = controls[0];
        var last = controls[controls.length - 1];
        if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
        else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
      }
    });

    theme.addEventListener('change', function () {
      save('theme', theme.value);
      if (storageAvailable) window.location.reload();
      else applyAppearance();
    });
    [[texture, 'texture'], [style, 'style'], [font, 'font']].forEach(function (setting) {
      setting[0].addEventListener('change', function () {
        save(setting[1], setting[0].value);
        applyAppearance();
      });
    });
    document.getElementById('reset-appearance').addEventListener('click', function () {
      save('theme', 'auto');
      save('texture', 'paper');
      save('style', 'balanced');
      save('font', 'system');
      window.location.reload();
    });
  }
})();
