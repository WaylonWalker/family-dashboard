/* Apply the saved palette before the browser paints the page. */
(function () {
  'use strict';
  var root = document.documentElement;
  var selected = 'auto';
  try { selected = window.localStorage.getItem('familyhq.theme') || 'auto'; }
  catch (e) { /* Use the automatic palette when storage is unavailable. */ }

  var themes = ['auto','everforest-light','everforest-dark','catppuccin-latte','catppuccin-mocha','gruvbox-light','gruvbox-dark','kanagawa-lotus','kanagawa-wave'];
  if (themes.indexOf(selected) === -1) selected = 'auto';
  if (selected === 'auto') {
    var hour = new Date().getHours();
    var weather = root.getAttribute('data-weather') || '';
    var rainy = weather === 'rain' || weather === 'storm' || weather === 'snow';
    if (hour < 6 || hour >= 20) selected = rainy ? 'kanagawa-wave' : 'catppuccin-mocha';
    else if (hour < 9 || hour >= 17) selected = rainy ? 'gruvbox-dark' : 'everforest-dark';
    else selected = rainy ? 'kanagawa-lotus' : 'everforest-light';
  }
  root.setAttribute('data-theme', selected);
  var themeColor = getComputedStyle(root).getPropertyValue('--bg').trim();
  var themeColorMeta = document.querySelector('meta[name="theme-color"]');
  if (themeColorMeta && themeColor) themeColorMeta.setAttribute('content', themeColor);
})();
