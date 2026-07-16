const OPEN_CLASS = 'open';
const OPEN_KEYS = new Set(['ArrowDown', 'Enter', ' ', 'Space', 'Spacebar']);

// createExportMenuController owns the complete menu-button focus contract so
// mouse, keyboard, and export activation paths cannot drift apart.
export function createExportMenuController({ button, menu, nextFocusTarget, onActivate }) {
    if (!button || !menu) {
        throw new Error('export menu button and menu are required');
    }

    const items = () => [...menu.querySelectorAll('[role="menuitem"]')];
    const isOpen = () => button.getAttribute('aria-expanded') === 'true';
    let keyboardActivation = false;

    function settleKeyboardFocus() {
        const settle = () => {
            if (isOpen()) items()[0]?.focus();
            keyboardActivation = false;
        };
        const view = button.ownerDocument?.defaultView;
        if (view?.requestAnimationFrame) view.requestAnimationFrame(settle);
        else setTimeout(settle, 0);
    }

    function setOpen(open, options = {}) {
        const nextOpen = !!open;
        menu.classList.toggle(OPEN_CLASS, nextOpen);
        button.setAttribute('aria-expanded', nextOpen ? 'true' : 'false');
        if (nextOpen && options.focusFirst) {
            items()[0]?.focus();
        } else if (!nextOpen && options.restoreFocus) {
            button.focus();
        }
    }

    function handleButtonKeydown(event) {
        if (OPEN_KEYS.has(event.key)) {
            event.preventDefault();
            keyboardActivation = event.key !== 'ArrowDown';
            // Keep focus on the button until keyup so Space and Enter complete
            // their native activation sequence on the same control.
            setOpen(true);
        } else if (event.key === 'Escape' && isOpen()) {
            event.preventDefault();
            setOpen(false, { restoreFocus: true });
        }
    }

    function handleButtonKeyup(event) {
        if (!OPEN_KEYS.has(event.key)) return;
        event.preventDefault();
        setOpen(true, { focusFirst: true });
        settleKeyboardFocus();
    }

    function handleMenuKeydown(event) {
        const menuItems = items();
        const current = menuItems.indexOf(event.currentTarget.ownerDocument.activeElement);
        if (event.key === 'Tab') {
            event.preventDefault();
            setOpen(false);
            if (event.shiftKey) button.focus();
            else (nextFocusTarget || button).focus();
        } else if (event.key === 'Escape') {
            event.preventDefault();
            setOpen(false, { restoreFocus: true });
        } else if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
            event.preventDefault();
            const direction = event.key === 'ArrowDown' ? 1 : -1;
            const start = current >= 0 ? current : (direction > 0 ? -1 : 0);
            const next = (start + direction + menuItems.length) % menuItems.length;
            menuItems[next]?.focus();
        } else if (event.key === 'Home' || event.key === 'End') {
            event.preventDefault();
            menuItems[event.key === 'Home' ? 0 : menuItems.length - 1]?.focus();
        }
    }

    button.addEventListener('click', (event) => {
        const keyboardLikeActivation = keyboardActivation || event.detail === 0;
        setOpen(keyboardLikeActivation ? true : !isOpen(), { focusFirst: keyboardLikeActivation });
        if (keyboardLikeActivation) settleKeyboardFocus();
    });
    button.addEventListener('keydown', handleButtonKeydown);
    button.addEventListener('keyup', handleButtonKeyup);
    menu.addEventListener('keydown', handleMenuKeydown);
    items().forEach((item) => {
        item.addEventListener('click', () => {
            try {
                onActivate?.(item.dataset.exportFormat);
            } finally {
                setOpen(false, { restoreFocus: true });
            }
        });
    });
    setOpen(false);

    return { setOpen, isOpen };
}
