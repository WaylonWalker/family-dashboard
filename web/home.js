(function () {
  'use strict';
  var status = document.getElementById('auth-status');
  var dashboard = document.getElementById('dashboard-link');
  var devices = document.getElementById('devices-link');

  function showSignedOut() {
    status.textContent = 'Not signed in yet. Sign in here, or pair a display with your phone.';
    dashboard.textContent = 'Sign in to Family HQ';
    devices.hidden = true;
  }

  var request = new XMLHttpRequest();
  request.open('GET', '/session', true);
  request.setRequestHeader('Accept', 'application/json');
  request.onreadystatechange = function () {
    if (request.readyState !== 4) return;
    if (request.status !== 200) {
      showSignedOut();
      return;
    }
    try {
      var session = JSON.parse(request.responseText);
      if (!session.authenticated) {
        showSignedOut();
        return;
      }
      status.textContent = 'You’re signed in.';
      dashboard.textContent = 'Open Family HQ';
      devices.hidden = !session.canManage;
    } catch (error) {
      showSignedOut();
    }
  };
  request.onerror = showSignedOut;
  request.send(null);
})();
