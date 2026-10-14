// A MudBlazor tip is placed on the side of its control it was asked for, and flipped to the other
// side when that one has no room; it is never moved along the side it is on. Beside a control near
// the edge of the window, one ran past it and widened the page. This slides an open tip back inside
// the window along its own side, and changes nothing else about it.
(function () {
    var MARGIN = 8;

    // How far to move a box so it lies inside the window less a margin. One wider than that starts
    // at the left margin, so its first words are the ones read.
    function shift(rect, width) {
        if (rect.width + 2 * MARGIN >= width || rect.left < MARGIN) return MARGIN - rect.left;
        if (rect.right > width - MARGIN) return width - MARGIN - rect.right;
        return 0;
    }

    // Measured from where MudBlazor placed it, which is where it is now less the move already on
    // it: fitting again changes nothing, and a tip placed again is fitted from its new place.
    // Compared as numbers, because a browser reads a translate back in its own spelling.
    function fit(tip) {
        var current = parseFloat(tip.style.translate) || 0;
        var rect = tip.getBoundingClientRect();
        var placed = { left: rect.left - current, right: rect.right - current, width: rect.width };
        var dx = Math.round(shift(placed, document.documentElement.clientWidth));
        if (dx !== current) tip.style.translate = dx ? dx + 'px' : '';
    }

    window.abomTipFit = { shift: shift, fit: fit };

    if (typeof MutationObserver === 'undefined' || !document.body) return;

    var pending = false;
    function fitOpen() {
        pending = false;
        var open = document.querySelectorAll('.mud-tooltip.mud-popover-open');
        for (var i = 0; i < open.length; i++) fit(open[i]);
    }
    function soon() {
        if (pending) return;
        pending = true;
        requestAnimationFrame(fitOpen);
    }
    // A tip opens by gaining a class and is placed by its style, both on the tip itself.
    new MutationObserver(function (records) {
        for (var i = 0; i < records.length; i++) {
            var target = records[i].target;
            if (target.classList && target.classList.contains('mud-tooltip')) {
                soon();
                return;
            }
        }
    }).observe(document.body, { subtree: true, attributes: true, attributeFilter: ['class', 'style'] });
    window.addEventListener('resize', soon);
})();
