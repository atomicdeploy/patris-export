import { iconMarkup, tableLanguage, tableText } from './table-ux.js';

const CONFIG_STORAGE_KEY = 'patris-config';
const SETTINGS_STORAGE_KEY = 'patris-settings';

function safeJSON(value) {
    try {
        const parsed = JSON.parse(value || 'null');
        return parsed && typeof parsed === 'object' ? parsed : null;
    } catch {
        return null;
    }
}

function storageJSON(storage, key) {
    if (!storage?.getItem) return null;
    return safeJSON(storage.getItem(key));
}

export function standaloneText(language, key, values = {}) {
    return tableText(language, key, values);
}

export function standaloneLanguage(value) {
    return tableLanguage(String(value || '').toLowerCase().startsWith('fa') ? 'fa' : value);
}

export function resolveStandaloneLanguage({ config, settings, navigatorLanguage = '' } = {}) {
    return standaloneLanguage(config?.ui?.language || settings?.language || navigatorLanguage);
}

export function resolveStandaloneTheme({ config, storedTheme = '', prefersDark = false } = {}) {
    const candidate = String(config?.ui?.theme || storedTheme || 'system').toLowerCase();
    if (candidate === 'dark' || candidate === 'light') return candidate;
    return prefersDark ? 'dark' : 'light';
}

export function pageIcon(name, className = '') {
    return iconMarkup(name, className);
}

export function localizeStandalonePage(root, language) {
    const locale = tableLanguage(language);
    root.querySelectorAll('[data-page-i18n]').forEach(element => {
        element.textContent = tableText(locale, element.dataset.pageI18n);
    });
    root.querySelectorAll('[data-page-i18n-placeholder]').forEach(element => {
        element.setAttribute('placeholder', tableText(locale, element.dataset.pageI18nPlaceholder));
    });
    root.querySelectorAll('[data-page-i18n-title]').forEach(element => {
        element.setAttribute('title', tableText(locale, element.dataset.pageI18nTitle));
    });
    root.querySelectorAll('[data-page-i18n-aria-label]').forEach(element => {
        element.setAttribute('aria-label', tableText(locale, element.dataset.pageI18nAriaLabel));
    });
    return locale;
}

export function applyStandaloneLocale(documentRef, language, titleKey) {
    const locale = localizeStandalonePage(documentRef, language);
    // Partial routes run behind the viewer's scoped document proxy. DOM
    // translation should remain scoped, but browser-owned document setters
    // must receive the real Document as their receiver.
    const pageDocument = documentRef.defaultView?.document || documentRef;
    pageDocument.documentElement.lang = locale;
    pageDocument.documentElement.dir = locale === 'fa' ? 'rtl' : 'ltr';
    pageDocument.title = tableText(locale, titleKey);
    return locale;
}

export async function loadStandalonePreferences({
    fetchImpl = globalThis.fetch,
    storage = globalThis.localStorage,
    navigatorLanguage = globalThis.navigator?.language || '',
    prefersDark = globalThis.matchMedia?.('(prefers-color-scheme: dark)').matches || false
} = {}) {
    const cachedConfig = storageJSON(storage, CONFIG_STORAGE_KEY);
    const settings = storageJSON(storage, SETTINGS_STORAGE_KEY);
    let config = cachedConfig;
    try {
        const response = await fetchImpl('/api/config', { headers: { Accept: 'application/json' } });
        if (response.ok) {
            config = await response.json();
            storage?.setItem?.(CONFIG_STORAGE_KEY, JSON.stringify(config));
        }
    } catch {
        // Standalone pages remain usable from their local cache while the API is unavailable.
    }
    return {
        config,
        language: resolveStandaloneLanguage({ config, settings, navigatorLanguage }),
        theme: resolveStandaloneTheme({
            config,
            storedTheme: storage?.getItem?.('theme') || '',
            prefersDark
        })
    };
}

function cacheStandaloneLanguage(storage, language, config) {
    const locale = tableLanguage(language);
    const settings = storageJSON(storage, SETTINGS_STORAGE_KEY) || {};
    storage?.setItem?.(SETTINGS_STORAGE_KEY, JSON.stringify({ ...settings, language: locale }));
    if (config) {
        storage?.setItem?.(CONFIG_STORAGE_KEY, JSON.stringify(config));
    }
}

export async function persistStandaloneLanguage(language, config, {
    fetchImpl = globalThis.fetch,
    storage = globalThis.localStorage
} = {}) {
    const locale = tableLanguage(language);
    const nextConfig = config && typeof config === 'object'
        ? { ...config, ui: { ...(config.ui || {}), language: locale } }
        : null;
    cacheStandaloneLanguage(storage, locale, nextConfig);
    if (!nextConfig || typeof fetchImpl !== 'function') return nextConfig;
    const response = await fetchImpl('/api/config', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(nextConfig)
    });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    return nextConfig;
}

export async function persistStandaloneTheme(theme, config, {
    fetchImpl = globalThis.fetch,
    storage = globalThis.localStorage
} = {}) {
    const normalized = theme === 'dark' ? 'dark' : 'light';
    storage?.setItem?.('theme', normalized);
    const nextConfig = config && typeof config === 'object'
        ? { ...config, ui: { ...(config.ui || {}), theme: normalized } }
        : null;
    if (nextConfig) storage?.setItem?.(CONFIG_STORAGE_KEY, JSON.stringify(nextConfig));
    if (!nextConfig || typeof fetchImpl !== 'function') return nextConfig;
    const response = await fetchImpl('/api/config', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(nextConfig)
    });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    return nextConfig;
}
