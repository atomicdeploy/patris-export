import {
    applyStandaloneLocale,
    loadStandalonePreferences,
    pageIcon,
    persistStandaloneLanguage,
    persistStandaloneTheme,
    standaloneText
} from './standalone-runtime.js';

const state = {
    config: null,
    language: 'en',
    theme: 'light'
};

const languageSelect = document.getElementById('languageSelect');
const themeToggle = document.getElementById('themeToggle');

function t(key, values = {}) {
    return standaloneText(state.language, key, values);
}

function renderIcons() {
    document.querySelectorAll('[data-page-icon]').forEach(element => {
        element.innerHTML = pageIcon(element.dataset.pageIcon, element.dataset.pageIconClass || '');
    });
}

function applyLanguage() {
    state.language = applyStandaloneLocale(document, state.language, 'homeDocumentTitle');
    languageSelect.value = state.language;
    updateThemeButton();
}

function updateThemeButton() {
    const isDark = state.theme === 'dark';
    document.body.classList.toggle('dark-mode', isDark);
    themeToggle.innerHTML = pageIcon(isDark ? 'sun' : 'moon');
    const actionLabel = t(isDark ? 'useLightTheme' : 'useDarkTheme');
    themeToggle.setAttribute('aria-label', actionLabel);
    themeToggle.setAttribute('title', actionLabel);
}

async function setLanguage(language) {
    state.language = language;
    applyLanguage();
    try {
        state.config = await persistStandaloneLanguage(state.language, state.config);
    } catch (error) {
        console.warn('Could not persist the standalone-page language:', error);
    }
}

async function toggleTheme() {
    state.theme = state.theme === 'dark' ? 'light' : 'dark';
    updateThemeButton();
    try {
        state.config = await persistStandaloneTheme(state.theme, state.config);
    } catch (error) {
        console.warn('Could not persist the standalone-page theme:', error);
    }
}

async function init() {
    const preferences = await loadStandalonePreferences();
    state.config = preferences.config;
    state.language = preferences.language;
    state.theme = preferences.theme;
    renderIcons();
    applyLanguage();
    updateThemeButton();

    languageSelect.addEventListener('change', event => setLanguage(event.target.value));
    themeToggle.addEventListener('click', toggleTheme);
}

if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init, { once: true });
} else {
    init();
}
