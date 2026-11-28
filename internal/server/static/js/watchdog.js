// Classic script (no module imports): it must survive a failure in app.js or
// character.js, which is exactly when the status would otherwise stick at the
// initial "Connecting..." label forever.
(function () {
    function installWatchdog(timeoutMs) {
        setTimeout(function () {
            const status = document.getElementById('statusText');
            if (!status || status.textContent.trim() !== 'Connecting...') return;

            status.textContent = 'Offline — check server';
            const badge = document.getElementById('statusIndicator');
            if (badge) badge.className = 'status-indicator offline';

            const scheme = location.protocol === 'https:' ? 'wss:' : 'ws:';
            console.error(
                '[Mai] No WebSocket connection after ' + timeoutMs + 'ms. Expected ' +
                scheme + '//' + location.host + '/ws. Open http://localhost:8080 ' +
                '(not file://) and start the server with "mai.exe --companion".'
            );
        }, timeoutMs);
    }

    window.installMaiWatchdog = installWatchdog;
})();
