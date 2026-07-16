import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const source = await readFile(new URL('../src/export-menu.js', import.meta.url), 'utf8');
const { createExportMenuController } = await import(`data:text/javascript;base64,${Buffer.from(source).toString('base64')}`);

class FakeClassList {
    constructor() {
        this.values = new Set();
    }

    toggle(value, enabled) {
        if (enabled) this.values.add(value);
        else this.values.delete(value);
    }

    contains(value) {
        return this.values.has(value);
    }
}

class FakeElement {
    constructor(ownerDocument, options = {}) {
        this.ownerDocument = ownerDocument;
        this.id = options.id || '';
        this.dataset = options.dataset || {};
        this.classList = new FakeClassList();
        this.attributes = new Map();
        this.listeners = new Map();
        this.menuItems = [];
    }

    addEventListener(type, callback) {
        const callbacks = this.listeners.get(type) || [];
        callbacks.push(callback);
        this.listeners.set(type, callbacks);
    }

    setAttribute(name, value) {
        this.attributes.set(name, String(value));
    }

    getAttribute(name) {
        return this.attributes.get(name) ?? null;
    }

    querySelectorAll(selector) {
        assert.equal(selector, '[role="menuitem"]');
        return this.menuItems;
    }

    focus() {
        this.ownerDocument.activeElement = this;
    }

    dispatch(type, values = {}) {
        const event = {
            key: values.key,
            detail: values.detail ?? (type === 'click' ? 1 : 0),
            shiftKey: !!values.shiftKey,
            currentTarget: this,
            target: this,
            defaultPrevented: false,
            preventDefault() {
                this.defaultPrevented = true;
            }
        };
        for (const callback of this.listeners.get(type) || []) {
            callback(event);
        }
        return event;
    }
}

function setupMenu() {
    const ownerDocument = { activeElement: null };
    const button = new FakeElement(ownerDocument, { id: 'exportBtn' });
    const menu = new FakeElement(ownerDocument, { id: 'exportDropdown' });
    const nextFocusTarget = new FakeElement(ownerDocument, { id: 'columnsBtn' });
    const items = ['json', 'csv', 'xlsx'].map((format) => new FakeElement(ownerDocument, {
        dataset: { exportFormat: format }
    }));
    menu.menuItems = items;
    const activations = [];
    const controller = createExportMenuController({
        button,
        menu,
        nextFocusTarget,
        onActivate: (format) => activations.push(format)
    });
    return { ownerDocument, button, menu, nextFocusTarget, items, activations, controller };
}

for (const [label, key] of [['Enter', 'Enter'], ['Space', ' '], ['Space key alias', 'Space'], ['ArrowDown', 'ArrowDown']]) {
    test(`${label} opens the export menu and moves focus to its first item`, () => {
        const { ownerDocument, button, menu, items } = setupMenu();
        button.focus();
        const keydown = button.dispatch('keydown', { key });
        if (key === 'Enter') button.dispatch('click', { detail: 0 });
        const keyup = button.dispatch('keyup', { key });
        if (key === ' ' || key === 'Space') button.dispatch('click', { detail: 0 });
        assert.equal(keydown.defaultPrevented, true);
        assert.equal(keyup.defaultPrevented, true);
        assert.equal(button.getAttribute('aria-expanded'), 'true');
        assert.equal(menu.classList.contains('open'), true);
        assert.equal(ownerDocument.activeElement, items[0]);
    });
}

test('Escape closes the menu, synchronizes aria-expanded, and restores button focus', () => {
    const { ownerDocument, button, menu, items, controller } = setupMenu();
    controller.setOpen(true, { focusFirst: true });
    items[1].focus();
    const event = menu.dispatch('keydown', { key: 'Escape' });
    assert.equal(event.defaultPrevented, true);
    assert.equal(button.getAttribute('aria-expanded'), 'false');
    assert.equal(menu.classList.contains('open'), false);
    assert.equal(ownerDocument.activeElement, button);
});

for (const shiftKey of [false, true]) {
    test(`${shiftKey ? 'Shift+Tab' : 'Tab'} closes the menu and moves focus outside it`, () => {
        const { ownerDocument, button, menu, nextFocusTarget, items, controller } = setupMenu();
        controller.setOpen(true, { focusFirst: true });
        items[shiftKey ? 0 : 2].focus();
        const event = menu.dispatch('keydown', { key: 'Tab', shiftKey });
        assert.equal(event.defaultPrevented, true);
        assert.equal(button.getAttribute('aria-expanded'), 'false');
        assert.equal(menu.classList.contains('open'), false);
        assert.equal(ownerDocument.activeElement, shiftKey ? button : nextFocusTarget);
    });
}

test('activating an export item closes the menu and restores focus to #exportBtn', () => {
    const { ownerDocument, button, menu, items, activations, controller } = setupMenu();
    controller.setOpen(true, { focusFirst: true });
    items[2].focus();
    items[2].dispatch('click');
    assert.deepEqual(activations, ['xlsx']);
    assert.equal(button.getAttribute('aria-expanded'), 'false');
    assert.equal(menu.classList.contains('open'), false);
    assert.equal(ownerDocument.activeElement, button);
});
