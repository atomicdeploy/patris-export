'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { execFileSync, spawnSync } = require('node:child_process');

const root = path.resolve(__dirname, '../..');
const files = ['pricing-sync.cjs', 'pricing-sync.cmd', 'README.md'];

test('runtime command files are required in both release archives and the installed Windows payload', () => {
  const packageScript = fs.readFileSync(path.join(root, 'scripts/package-release.sh'), 'utf8');
  const installer = fs.readFileSync(path.join(root, 'installer/windows/patris-export.nsi'), 'utf8');
  const builder = fs.readFileSync(path.join(root, 'scripts/windows/Build-Installer.ps1'), 'utf8');
  assert.match(packageScript, /for stage in "\$windows_stage" "\$linux_stage"; do\s+mkdir -p -- "\$stage\/scripts\/pricing"/);
  assert.match(packageScript, /for pricing_file in pricing-sync\.cjs pricing-sync\.cmd README\.md/);
  assert.match(packageScript, /install -m 0644 "\$root\/scripts\/pricing\/\$pricing_file" "\$stage\/scripts\/pricing\/\$pricing_file"/);
  assert.ok(installer.includes('SetOutPath "$INSTDIR\\scripts\\pricing"'));
  for (const file of files) {
    assert.ok(installer.includes(`File /oname=${file} "\${PAYLOAD_DIR}\\scripts\\pricing\\${file}"`));
    assert.ok(installer.includes(`Delete "$INSTDIR\\scripts\\pricing\\${file}"`));
    assert.ok(builder.includes(`"scripts\\pricing\\${file}"`));
  }
  assert.match(packageScript, /sha256sum "\$windows_archive" "\$linux_archive" > SHA256SUMS/);
});

test('existing release packager produces identical archives with the exact CLI payload', t => {
  const bash = process.env.PRICING_TEST_BASH || (process.platform === 'win32'
    ? 'C:/Program Files/Git/usr/bin/bash.exe' : 'bash');
  const tools = spawnSync(bash, ['-c', 'command -v zip >/dev/null && command -v unzip >/dev/null'], { encoding: 'utf8' });
  if (tools.status !== 0) { t.skip('The real archive test requires Bash, zip and unzip.'); return; }
  const build = path.join(root, 'build');
  fs.mkdirSync(build, { recursive: true });
  const temporary = fs.mkdtempSync(path.join(build, 'pricing-package-test-'));
  t.after(() => {
    assert.equal(path.dirname(path.resolve(temporary)), path.resolve(build));
    fs.rmSync(temporary, { recursive: true, force: true });
  });
  const input = path.join(temporary, 'input');
  for (const [platform, names] of Object.entries({
    windows: ['patris-export-windows-amd64.exe', 'patris-export.dll', 'patris-export.h', 'libpxlib.dll', 'libgcc_s_seh-1.dll', 'libstdc++-6.dll', 'libwinpthread-1.dll'],
    linux: ['patris-export-linux-amd64', 'libpatris-export.so', 'libpatris-export.h', 'libpx.so.1'],
  })) {
    fs.mkdirSync(path.join(input, platform), { recursive: true });
    for (const name of names) fs.writeFileSync(path.join(input, platform, name), 'fixture:' + name + '\n');
  }
  const commit = execFileSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8' }).trim();
  const outputs = [path.join(temporary, 'one'), path.join(temporary, 'two')];
  const shellPath = value => value.replaceAll('\\', '/');
  for (const output of outputs) {
    execFileSync(bash, ['scripts/package-release.sh', 'candidate', commit, shellPath(input), shellPath(output)], { cwd: root, stdio: 'pipe' });
  }
  assert.equal(fs.readFileSync(path.join(outputs[0], 'SHA256SUMS'), 'utf8'), fs.readFileSync(path.join(outputs[1], 'SHA256SUMS'), 'utf8'));
  const archive = fs.readdirSync(outputs[0]).find(name => name.endsWith('.zip'));
  for (const file of files) {
    const bytes = execFileSync(bash, ['-c', 'unzip -p "$1" "$2"', 'verify', shellPath(path.join(outputs[0], archive)), 'patris-export-windows-amd64/scripts/pricing/' + file]);
    assert.deepEqual(bytes, fs.readFileSync(path.join(__dirname, file)));
  }
  const linux = fs.readdirSync(outputs[0]).find(name => name.endsWith('.tar.gz'));
  for (const file of files) {
    const bytes = execFileSync(bash, ['-c', 'tar -xOf "$1" "$2"', 'verify', shellPath(path.join(outputs[0], linux)), 'patris-export-linux-amd64/scripts/pricing/' + file]);
    assert.deepEqual(bytes, fs.readFileSync(path.join(__dirname, file)));
  }
});
