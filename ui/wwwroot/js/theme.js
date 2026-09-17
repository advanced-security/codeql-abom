window.abomTheme = {
    load: function (key) {
        try { return localStorage.getItem(key); } catch (e) { return null; }
    },
    // Every preference is saved through here, so it must not read a color scheme out of the
    // value: a refresh interval of "60" is not "dark", and repainting on it inverted the page.
    save: function (key, value) {
        try { localStorage.setItem(key, value); } catch (e) { /* private mode */ }
    },
    /** The one thing that repaints the page, so the attribute cannot disagree with the theme. */
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
