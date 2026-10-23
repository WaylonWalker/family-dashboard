/* Apply the saved palette before the browser paints the page. */
(function () {
  'use strict';
  var root = document.documentElement;
  var selected = 'auto';
  try { selected = window.localStorage.getItem('familyhq.theme') || 'auto'; }
  catch (e) { /* Use the automatic palette when storage is unavailable. */ }

  var themes = ["auto","2bit-demichrome","ash","autumn","ayu-dark","ayu-light","ayu-mirage","berry-nebula","black-gold","blessing","catppuccin-frappe","catppuccin-latte","catppuccin-macchiato","catppuccin-mocha","christmas","default-dark","default-light","diwali","dracula","everforest-dark","everforest-light","graphite-dark","graphite-light","gruvbox-dark","gruvbox-light","halloween","hanukkah","ink","inkpink","kanagawa-dragon","kanagawa-lotus","kanagawa-wave","lava-gb","lunar-new-year","matte-black","midnight-ablaze","mist-gb","modus-operandi","modus-vivendi","monokai","night-owl-light","night-owl","nintendo-gameboy","nord-dark","nord-light","oil-6","one-dark","one-light","pico-8","pitch-black","pollen8","rose-pine-dawn","rose-pine-moon","rose-pine","rust-gold-8","slso8","solarized-dark","solarized-light","st.-patrick's-day","summer-beach","sweet","tokyo-night-day","tokyo-night-storm","tokyo-night","twilight-5","valentine","vinik24","white-gold","whitesur-dark","whitesur-light","winter-frost","2bit-demichrome-light","ash-light","autumn-light","ayu-mirage-light","berry-nebula-light","black-gold-light","blessing-dark","catppuccin-frappe-light","catppuccin-latte-dark","catppuccin-macchiato-light","catppuccin-mocha-light","christmas-light","diwali-light","dracula-light","halloween-light","hanukkah-light","ink-light","inkpink-light","kanagawa-dragon-light","kanagawa-lotus-dark","kanagawa-wave-light","lava-gb-light","lunar-new-year-light","matte-black-light","midnight-ablaze-light","mist-gb-light","modus-operandi-dark","modus-vivendi-light","monokai-light","nintendo-gameboy-light","oil-6-dark","pico-8-light","pitch-black-light","pollen8-dark","rose-pine-dawn-dark","rose-pine-moon-light","rose-pine-light","rust-gold-8-light","slso8-light","st.-patrick's-day-light","summer-beach-dark","sweet-light","tokyo-night-day-dark","tokyo-night-storm-light","tokyo-night-light","twilight-5-light","valentine-light","vinik24-light","white-gold-dark","winter-frost-light","8008","8008-light","aether","aether-light","bento","bento-light","olivia","olivia-light","serika-dark","serika-light","system-dark","system","paper-dark","paper"];
  if (themes.indexOf(selected) === -1) selected = 'auto';
  if (selected === 'auto') selected = automaticTheme(new Date(), root.getAttribute('data-weather') || '');
  root.setAttribute('data-theme', selected);
  var themeColor = getComputedStyle(root).getPropertyValue('--bg').trim();
  var themeColorMeta = document.querySelector('meta[name="theme-color"]');
  if (themeColorMeta && themeColor) themeColorMeta.setAttribute('content', themeColor);

  function automaticTheme(date, weather) {
    var md = ('0' + (date.getMonth() + 1)).slice(-2) + '-' + ('0' + date.getDate()).slice(-2);
    var year = date.getFullYear();
    var easter = easterDate(year);
    var thanksgiving = new Date(year, 10, 1);
    thanksgiving.setDate(1 + ((4 - thanksgiving.getDay() + 7) % 7) + 21);
    // Fixed annual date ranges, shaped after Markata Go's theme calendar.
    // Ranges include the three days leading into each holiday.
    var holidays = [
      {start:'12-29', end:'01-01', palette:'gruvbox-dark'},
      {start:'02-11', end:'02-14', palette:'catppuccin-mocha'},
      {start:'03-14', end:'03-17', palette:'everforest-dark'},
      {start:'04-19', end:'04-22', palette:'everforest-dark'},
      {start:'10-28', end:'10-31', palette:'kanagawa-wave'},
      {start:'12-22', end:'12-25', palette:'gruvbox-dark'}
    ];
    holidays.push({start:dateKey(addDays(easter, -3)), end:dateKey(easter), palette:'catppuccin-mocha'});
    holidays.push({start:dateKey(addDays(thanksgiving, -3)), end:dateKey(thanksgiving), palette:'gruvbox-dark'});
    for (var i = 0; i < holidays.length; i++) {
      var h = holidays[i];
      if (h.start <= h.end ? md >= h.start && md <= h.end : md >= h.start || md <= h.end) return h.palette;
    }
    if (weather === 'snow') return 'pitch-black';
    return weather === 'rain' || weather === 'storm' ? 'kanagawa-wave' : 'everforest-dark';
  }
  function addDays(value, days) { var result = new Date(value.getTime()); result.setDate(result.getDate() + days); return result; }
  function dateKey(value) { return ('0' + (value.getMonth() + 1)).slice(-2) + '-' + ('0' + value.getDate()).slice(-2); }
  function easterDate(year) {
    var a=year%19,b=Math.floor(year/100),c=year%100,d=Math.floor(b/4),e=b%4,f=Math.floor((b+8)/25),g=Math.floor((b-f+1)/3);
    var h=(19*a+b-d-g+15)%30,i=Math.floor(c/4),k=c%4,l=(32+2*e+2*i-h-k)%7,m=Math.floor((a+11*h+22*l)/451);
    var month=Math.floor((h+l-7*m+114)/31),day=((h+l-7*m+114)%31)+1;
    return new Date(year, month-1, day);
  }
})();
