window.abomTheme = {
    load: function (key) {
        try { return localStorage.getItem(key); } catch (e) { return null; }
    },
    save: function (key, value) {
        try { localStorage.setItem(key, value); } catch (e) { /* private mode */ }
        // Keep the pre-paint attribute in step so a reload does not flash the other scheme.
        var dark = value === 'dark' || (value === 'system' &&
            window.matchMedia('(prefers-color-scheme: dark)').matches);
        document.documentElement.setAttribute('data-theme', dark ? 'dark' : 'light');
    },
    applyResolved: function (isDark) {
        document.documentElement.setAttribute('data-theme', isDark ? 'dark' : 'light');
    },
    /** Reports the element's box so the canvas graph can size itself to its container. */
    measure: function (id) {
        var el = document.getElementById(id);
        if (!el) return { width: 0, height: 0 };
        var r = el.getBoundingClientRect();
        return { width: Math.round(r.width), height: Math.round(r.height) };
    }
};
