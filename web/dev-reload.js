(function () {
  var version = null;
  var socket = null;
  var retryDelay = 500;
  var fallbackTimer = null;
  var reconnectTimer = null;
  var connectTimer = null;

  function checkVersion(nextVersion) {
    if (version !== null && nextVersion !== version) {
      window.location.reload();
      return;
    }
    version = nextVersion;
  }

  // Keep an XHR fallback for older Silk builds and networks that block upgrades.
  function poll() {
    var request = new XMLHttpRequest();
    var finished = false;
    request.open('GET', '/__dev/ping', true);
    request.setRequestHeader('Cache-Control', 'no-cache');
    request.onreadystatechange = function () {
      if (request.readyState !== 4 || finished) return;
      finished = true;
      if (request.status === 200) checkVersion(request.responseText);
      if (!socket || socket.readyState !== 1) fallbackTimer = window.setTimeout(poll, 5000);
    };
    request.onerror = function () {
      if (finished) return;
      finished = true;
      if (!socket || socket.readyState !== 1) fallbackTimer = window.setTimeout(poll, 5000);
    };
    request.send(null);
  }

  function connect() {
    if (!window.WebSocket) {
      if (fallbackTimer === null) poll();
      return;
    }

    var scheme = window.location.protocol === 'https:' ? 'wss://' : 'ws://';
    try {
      socket = new WebSocket(scheme + window.location.host + '/__dev/ws');
    } catch (_) {
      scheduleReconnect();
      return;
    }

    socket.onopen = function () {
      retryDelay = 500;
      if (connectTimer !== null) {
        window.clearTimeout(connectTimer);
        connectTimer = null;
      }
      if (fallbackTimer !== null) {
        window.clearTimeout(fallbackTimer);
        fallbackTimer = null;
      }
    };
    socket.onmessage = function (event) {
      checkVersion(event.data);
    };
    socket.onerror = function () {
      if (connectTimer !== null) {
        window.clearTimeout(connectTimer);
        connectTimer = null;
      }
      if (fallbackTimer === null) poll();
    };
    socket.onclose = function () {
      socket = null;
      if (connectTimer !== null) {
        window.clearTimeout(connectTimer);
        connectTimer = null;
      }
      if (fallbackTimer === null) poll();
      scheduleReconnect();
    };
    connectTimer = window.setTimeout(function () {
      connectTimer = null;
      if (socket && socket.readyState === WebSocket.CONNECTING && fallbackTimer === null) poll();
    }, 3000);
  }

  function scheduleReconnect() {
    if (reconnectTimer !== null) return;
    reconnectTimer = window.setTimeout(function () {
      reconnectTimer = null;
      connect();
    }, retryDelay);
    retryDelay = Math.min(retryDelay * 2, 5000);
  }

  connect();
})();
