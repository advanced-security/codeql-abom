window.abomTutorial = (() => {
    let active = null;
    const focusable = 'button, input, select, textarea, a[href], summary, [tabindex]';

    function clip(rect, width, height) {
        const x = Math.max(0, rect.left - 6);
        const y = Math.max(0, rect.top - 6);
        const right = Math.min(width, rect.right + 6);
        const bottom = Math.min(height, rect.bottom + 6);
        return right > x && bottom > y ? { x, y, width: right - x, height: bottom - y } : null;
    }

    function place(box, width, height, viewportWidth, viewportHeight) {
        const clamp = (n, max) => Math.max(8, Math.min(n, max - 8));
        const candidates = box ? [
            { x: box.x + box.width + 16, y: clamp(box.y, viewportHeight - height) },
            { x: box.x - width - 16, y: clamp(box.y, viewportHeight - height) },
            { x: clamp(box.x, viewportWidth - width), y: box.y + box.height + 16 },
            { x: clamp(box.x, viewportWidth - width), y: box.y - height - 16 },
        ] : [];
        const fits = p => p.x >= 8 && p.y >= 8 &&
            p.x + width <= viewportWidth - 8 && p.y + height <= viewportHeight - 8;
        return candidates.find(fits) ?? {
            x: clamp(viewportWidth - width - 16, viewportWidth - width),
            y: clamp(viewportHeight - height - 16, viewportHeight - height),
        };
    }

    function rectInto(element, rect) {
        if (!element) return;
        for (const name of ['x', 'y', 'width', 'height'])
            element.setAttribute(name, String(rect?.[name] ?? 0));
    }

    function visible(element) {
        return element && element.getClientRects().length > 0 &&
            window.getComputedStyle(element).visibility !== 'hidden';
    }

    function restore(state) {
        for (const [element, inert] of state.inert) element.inert = inert;
        state.inert.clear();
        for (const [element, value] of state.disabled) {
            if (value === null) element.removeAttribute('aria-disabled');
            else element.setAttribute('aria-disabled', value);
        }
        state.disabled.clear();
        for (const element of state.marked) element.removeAttribute('data-abom-tutorial-active-action');
        state.marked.clear();
    }

    function activation(element, kind) {
        const value = kind === 'query'
            ? element.closest('tr')?.querySelector('[data-abom-query-id]')?.getAttribute('data-abom-query-id')
            : element.getAttribute(kind === 'finding' ? 'data-abom-review-finding' : 'data-abom-unit');
        return { kind, href: element.getAttribute('href') ?? '', value: value ?? '' };
    }

    function isolate(state, target, coach) {
        restore(state);
        const kept = [target, coach, document.querySelector('[data-abom-tutorial-notice]')].filter(Boolean);
        function walk(parent) {
            for (const child of parent.children) {
                if (kept.some(el => el === child || el.contains(child))) continue;
                if (kept.some(el => child.contains(el)) || child.matches('.abom-tutorial-scrim')) {
                    walk(child);
                } else if (child instanceof window.HTMLElement && !child.inert) {
                    state.inert.set(child, child.inert);
                    child.inert = true;
                }
            }
        }
        walk(document.body);
        if (target && !coach.contains(target)) {
            for (const control of [target, ...target.querySelectorAll(focusable)]) {
                if (!control.matches(focusable) || state.actions.includes(control)) continue;
                state.disabled.set(control, control.getAttribute('aria-disabled'));
                control.setAttribute('aria-disabled', 'true');
            }
        }
        for (const element of state.actions) {
            element.setAttribute('data-abom-tutorial-active-action', '');
            state.marked.add(element);
        }
    }

    function update(state) {
        if (active !== state) return;
        state.frame = 0;
        const coach = document.querySelector('[data-abom-tutorial-coach]');
        if (!coach) return;
        let actions = state.actionSelector
            ? [...document.querySelectorAll(state.actionSelector)].filter(el => visible(el) && !el.disabled) : [];
        if (state.kind !== 'evidence-tab' && actions.length) {
            const chosen = state.actionKey
                ? actions.find(el => JSON.stringify(activation(el, state.kind)) === state.actionKey)
                : actions[0];
            actions = chosen ? [chosen] : [];
            if (chosen) state.actionKey = JSON.stringify(activation(chosen, state.kind));
        }
        state.actions = actions;
        const primary = state.actionSelector === state.selector && state.kind
            ? actions[0] : [...document.querySelectorAll(state.selector)].find(visible);
        const target = primary ?? document.querySelector('.mud-main-content h1');
        if (target && state.scroll) {
            state.scroll = false;
            target.scrollIntoView({ block: 'nearest', inline: 'nearest', behavior: 'instant' });
        }
        const box = target ? clip(target.getBoundingClientRect(), window.innerWidth, window.innerHeight) : null;
        rectInto(document.querySelector('[data-abom-tutorial-hole]'), box);
        const preview = target?.closest('[data-abom-tutorial-preview]');
        const placementBox = preview ? clip(preview.getBoundingClientRect(), window.innerWidth, window.innerHeight) : box;
        const position = place(placementBox, coach.offsetWidth, coach.offsetHeight, window.innerWidth, window.innerHeight);
        coach.style.left = position.x + 'px';
        coach.style.top = position.y + 'px';
        isolate(state, target, coach);
        if (state.target !== target) {
            state.resize.disconnect();
            state.resize.observe(coach);
            if (target) state.resize.observe(target);
            state.target = target;
        }
        const loading = [...document.querySelectorAll('.mud-main-content [role="progressbar"]')].some(visible);
        const status = primary && (!state.kind || actions.length) ? 'ready' : loading ? 'loading' : 'missing';
        if (state.status !== status) {
            state.status = status;
            state.dotnet.invokeMethodAsync('TargetChanged', state.generation, status);
        }
        if (state.focus) {
            state.focus = false;
            coach.querySelector('h2')?.focus({ preventScroll: true });
        }
    }

    function queue(state) {
        if (!state.frame && active === state)
            state.frame = window.requestAnimationFrame(() => update(state));
    }

    function allowed(element, state) {
        return element instanceof window.Element &&
            (!!element.closest('[data-abom-tutorial-coach]') ||
                state.actions.some(action => action === element || action.contains(element)));
    }

    function show(selector, dotnet, generation, actionSelector = '', kind = '') {
        if (!active) {
            const state = {
                inert: new Map(), disabled: new Map(), marked: new Set(), actions: [], frame: 0, target: null,
                previousFocus: document.activeElement,
            };
            active = state;
            state.refresh = () => queue(state);
            state.resize = new window.ResizeObserver(state.refresh);
            state.mutations = new window.MutationObserver(state.refresh);
            state.mutations.observe(document.body, { childList: true, subtree: true });
            state.pointer = event => {
                const action = state.actions.find(el => el === event.target || el.contains(event.target));
                if (action && event.type === 'click') {
                    event.preventDefault();
                    event.stopImmediatePropagation();
                    if (!state.pending) {
                        state.pending = true;
                        state.dotnet.invokeMethodAsync('Activate', state.generation, activation(action, state.kind))
                            .then(accepted => { if (!accepted) state.pending = false; })
                            .catch(error => {
                                state.pending = false;
                                console.error('Tutorial navigation failed', error);
                            });
                    }
                    return;
                }
                if (allowed(event.target, state) && event.type !== 'contextmenu') return;
                event.preventDefault();
                event.stopImmediatePropagation();
            };
            state.key = event => {
                if (event.key === 'Escape') {
                    event.preventDefault();
                    event.stopImmediatePropagation();
                    state.dotnet.invokeMethodAsync('Escape');
                    return;
                }
                if (event.key === 'Tab') {
                    const items = [...state.actions, ...document.querySelector('[data-abom-tutorial-coach]').querySelectorAll(focusable)]
                        .filter(el => visible(el) && !el.disabled && el.tabIndex >= 0);
                    const index = items.indexOf(document.activeElement);
                    const next = event.shiftKey ? (index <= 0 ? items.length - 1 : index - 1)
                        : (index + 1) % items.length;
                    event.preventDefault();
                    event.stopImmediatePropagation();
                    items[next]?.focus();
                } else if (!allowed(event.target, state)) {
                    event.preventDefault();
                    event.stopImmediatePropagation();
                }
            };
            state.focusin = event => {
                if (!allowed(event.target, state))
                    document.querySelector('[data-abom-tutorial-coach] h2')?.focus({ preventScroll: true });
            };
            for (const event of ['pointerdown', 'click', 'dblclick', 'contextmenu', 'submit'])
                document.addEventListener(event, state.pointer, true);
            document.addEventListener('keydown', state.key, true);
            document.addEventListener('focusin', state.focusin, true);
            window.addEventListener('resize', state.refresh);
            window.addEventListener('scroll', state.refresh, true);
        }
        Object.assign(active, { selector, dotnet, generation, actionSelector, kind, actionKey: '',
            status: null, pending: false, scroll: true, focus: true });
        queue(active);
    }

    function end() {
        const state = active;
        if (!state) return;
        active = null;
        if (state.frame) window.cancelAnimationFrame(state.frame);
        state.resize.disconnect();
        state.mutations.disconnect();
        for (const event of ['pointerdown', 'click', 'dblclick', 'contextmenu', 'submit'])
            document.removeEventListener(event, state.pointer, true);
        document.removeEventListener('keydown', state.key, true);
        document.removeEventListener('focusin', state.focusin, true);
        window.removeEventListener('resize', state.refresh);
        window.removeEventListener('scroll', state.refresh, true);
        restore(state);
        if (state.previousFocus?.isConnected) state.previousFocus.focus({ preventScroll: true });
        else document.querySelector('[data-abom-tutorial-checklist] summary')?.focus({ preventScroll: true });
    }

    return {
        load: key => localStorage.getItem(key),
        save: (key, value) => localStorage.setItem(key, value),
        show, end, clip, place, activation,
    };
})();
