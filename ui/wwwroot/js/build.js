// A server restarted onto a new build keeps answering pages loaded from the old one, and those pages
// keep working, so nothing on them says what they are missing: a tab the server now offers looks
// absent rather than unloaded. This compares what the page booted from with what the server serves
// now, and says so when they differ. Only a positive difference counts; a server that cannot be
// reached, or an answer that is not the interface's own page, is not evidence of a newer build.
window.abomBuild = (function () {
    var notice = document.getElementById('abom-stale-build');
    var map = document.querySelector('script[type="importmap"]');
    var booted = map ? map.textContent.trim() : '';
    // First-party files loaded by a fixed name. The framework's are fingerprinted in the import
    // map, and the vendored libraries change only as a deliberate upgrade.
    var assets = Array.prototype.map.call(
        document.querySelectorAll('link[rel="stylesheet"][href], script[src]'),
        function (el) { return el.getAttribute('href') || el.getAttribute('src'); })
        .filter(function (path) { return /^(css|js)\//.test(path) && !/\.min\.js$/.test(path); });
    var dates = {};
    var stale = false;
    var busy = false;
    var last = 0;

    function served(html) {
        var found = /<script type="importmap">([\s\S]*?)<\/script>/.exec(html);
        return found ? found[1].trim() : '';
    }

    function dated() {
        return Promise.all(assets.map(function (path) {
            return fetch(path, { method: 'HEAD', cache: 'no-cache' }).then(function (res) {
                return res.ok ? res.headers.get('last-modified') || '' : '';
            }, function () { return ''; });
        })).then(function (values) {
            var now = {};
            assets.forEach(function (path, i) { now[path] = values[i]; });
            return now;
        });
    }

    var ready = dated().then(function (now) { dates = now; });

    function newer() {
        return fetch('./', { cache: 'no-cache' }).then(function (res) {
            return res.ok ? res.text() : '';
        }).then(function (html) {
            var current = served(html);
            if (booted && current && current !== booted) return true;
            return dated().then(function (now) {
                return assets.some(function (path) {
                    return dates[path] && now[path] && dates[path] !== now[path];
                });
            });
        });
    }

    function check() {
        if (stale) return Promise.resolve(true);
        if (busy) return Promise.resolve(false);
        busy = true;
        last = Date.now();
        return ready.then(newer).then(function (found) {
            busy = false;
            if (found) {
                stale = true;
                if (notice) notice.hidden = false;
            }
            return found;
        }, function () {
            busy = false;
            return false;
        });
    }

    // On the reader's return to the page as well as on a timer, so a tab left open across a
    // restart says so when it is looked at, not up to a minute later.
    function soon() {
        if (!document.hidden && Date.now() - last > 10000) check();
    }
    setInterval(soon, 60000);
    document.addEventListener('visibilitychange', soon);
    window.addEventListener('focus', soon);
    if (notice) {
        notice.querySelector('button').addEventListener('click', function () { location.reload(); });
    }

    return { check: check, ready: ready };
})();
