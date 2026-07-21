import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

import {
    applyStandaloneLocale,
    persistStandaloneLanguage,
    resolveStandaloneLanguage,
    resolveStandaloneTheme,
    standaloneText
} from '../src/standalone-runtime.js';
import { tableTranslationCoverage } from '../src/table-ux.js';

const testDirectory = path.dirname(fileURLToPath(import.meta.url));
const webRoot = path.resolve(testDirectory, '..');

function source(name) {
    return fs.readFileSync(path.join(webRoot, 'src', name), 'utf8');
}

function translationKeys(markup, script = '') {
    const staticKeys = [...markup.matchAll(/data-page-i18n(?:-(?:placeholder|title|aria-label))?="([^"]+)"/g)]
        .map(match => match[1]);
    const dynamicKeys = [...script.matchAll(/\bt\('([^']+)'/g)].map(match => match[1]);
    return [...new Set([...staticKeys, ...dynamicKeys])];
}

test('standalone pages use the shared complete English and Persian dictionary', () => {
    const coverage = tableTranslationCoverage();
    assert.deepEqual(coverage.missingEnglish, []);
    assert.deepEqual(coverage.missingPersian, []);

    const pages = [
        ['welcome.html', 'welcome.js'],
        ['charmap.html', 'charmap.js']
    ];
    for (const [htmlName, scriptName] of pages) {
        for (const key of translationKeys(source(htmlName), source(scriptName))) {
            assert.notEqual(standaloneText('en', key), key, `${htmlName} is missing English key ${key}`);
            assert.notEqual(standaloneText('fa', key), key, `${htmlName} is missing Persian key ${key}`);
        }
    }
});

test('standalone language follows server config, cache, then browser locale', () => {
    assert.equal(resolveStandaloneLanguage({
        config: { ui: { language: 'fa' } },
        settings: { language: 'en' },
        navigatorLanguage: 'en-US'
    }), 'fa');
    assert.equal(resolveStandaloneLanguage({ settings: { language: 'fa' }, navigatorLanguage: 'en-US' }), 'fa');
    assert.equal(resolveStandaloneLanguage({ navigatorLanguage: 'fa-IR' }), 'fa');
    assert.equal(resolveStandaloneLanguage({ navigatorLanguage: 'de-DE' }), 'en');
});

test('locale application scopes translations but uses the real document for browser setters', () => {
    const elements = [{ dataset: { pageI18n: 'home' }, textContent: '' }];
    const realDocument = { documentElement: { lang: '', dir: '' }, title: '' };
    const scopedDocument = {
        defaultView: { document: realDocument },
        querySelectorAll: selector => selector === '[data-page-i18n]' ? elements : []
    };
    assert.equal(applyStandaloneLocale(scopedDocument, 'fa', 'homeDocumentTitle'), 'fa');
    assert.equal(elements[0].textContent, 'صفحه اصلی');
    assert.equal(realDocument.documentElement.lang, 'fa');
    assert.equal(realDocument.documentElement.dir, 'rtl');
    assert.equal(realDocument.title, 'Patris Export - صفحه اصلی');
});

test('standalone theme respects explicit config and system fallback', () => {
    assert.equal(resolveStandaloneTheme({ config: { ui: { theme: 'dark' } }, storedTheme: 'light' }), 'dark');
    assert.equal(resolveStandaloneTheme({ storedTheme: 'light', prefersDark: true }), 'light');
    assert.equal(resolveStandaloneTheme({ config: { ui: { theme: 'system' } }, prefersDark: true }), 'dark');
});

test('language persistence preserves the complete server configuration', async () => {
    const values = new Map();
    const storage = {
        getItem: key => values.get(key) ?? null,
        setItem: (key, value) => values.set(key, value)
    };
    const requests = [];
    const config = { server: { port: 8080 }, ui: { language: 'en', theme: 'dark' } };
    const nextConfig = await persistStandaloneLanguage('fa', config, {
        storage,
        fetchImpl: async (url, options) => {
            requests.push({ url, options });
            return { ok: true };
        }
    });

    assert.deepEqual(nextConfig, { server: { port: 8080 }, ui: { language: 'fa', theme: 'dark' } });
    assert.equal(requests.length, 1);
    assert.equal(requests[0].url, '/api/config');
    assert.deepEqual(JSON.parse(requests[0].options.body), nextConfig);
    assert.equal(JSON.parse(values.get('patris-settings')).language, 'fa');
});

test('welcome page uses only allowlisted SVG primitives and keeps accessible labels', () => {
    const markup = source('welcome.html');
    const pictographs = markup.match(/\p{Extended_Pictographic}/gu) || [];
    assert.deepEqual(pictographs, [], 'raw emoji must not be used as interface icons');
    assert.doesNotMatch(markup, /(?:ðŸ|âš|â˜|â†)/u);
    assert.match(markup, /<button class="icon-button" id="themeToggle" type="button" aria-label=/);
    assert.match(markup, /<label class="sr-only" for="languageSelect"/);
    assert.match(markup, /data-page-icon="moon"/);
    assert.match(markup, /data-page-icon="refresh"/);
    assert.match(markup, /data-page-icon="search"/);
    assert.match(markup, /data-page-icon="columns"/);
    assert.match(markup, /data-page-icon="braces"/);
    assert.match(markup, /data-page-i18n-aria-label="capabilities"/);
    assert.doesNotMatch(markup, /on(?:click|change|load)=/i);
});

test('charmap page exposes semantic controls and localized dynamic states', () => {
    const markup = source('charmap.html');
    const script = source('charmap.js');
    assert.match(markup, /<caption class="sr-only" data-page-i18n="charmapPageTitle">/);
    assert.equal((markup.match(/<th scope="col"/g) || []).length, 5);
    assert.match(markup, /id="debugNotice" role="alert"/);
    assert.match(markup, /id="issues" role="status"/);
    assert.match(markup, /data-page-i18n-aria-label="primaryNavigation"/);
    assert.match(markup, /for="fileInput" data-page-i18n="chooseTextFile"/);
    assert.match(markup, /id="fileName" data-page-i18n="noFileSelected"/);
    assert.match(markup, /class="sr-only" id="fileInput"/);
    assert.match(script, /charmapIssueExpectedTab/);
    assert.match(script, /charmapIssueSingleByte/);
    assert.match(script, /charmapRequestFailed/);
    assert.doesNotMatch(markup, /on(?:click|change|load)=/i);
});

test('the build embeds font and scripts into both offline standalone pages', () => {
    const build = source('../build.js');
    assert.match(build, /bundle\('src\/welcome\.js'\)/);
    assert.match(build, /bundle\('src\/charmap\.js'\)/);
    assert.match(build, /finalWelcomeHtml[\s\S]*EMBEDDED_FONT[\s\S]*PAGE_SCRIPTS/);
    assert.match(build, /finalCharmapHtml[\s\S]*EMBEDDED_FONT[\s\S]*PAGE_SCRIPTS/);
    assert.equal((build.match(/\.replace\([^\n]+, \(\) =>/g) || []).length, 6,
        'embedded bundle replacements must not interpret JavaScript $ replacement tokens');
});
