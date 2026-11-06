const { test } = require('node:test');
const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { join } = require('node:path');
const { runInNewContext } = require('node:vm');

const source = readFileSync(join(__dirname, '../cmd/runner-manager/static/app.js'), 'utf8');
const start = source.indexOf('function sanitizeRunnerName(');
const end = source.indexOf("document.getElementById('parseCommandBtn')", start);
assert.ok(start >= 0 && end > start);
const parse = runInNewContext(source.slice(start, end) + '\nparseGitHubConfigCommand', { URL, t: key => key });

test('Windows PowerShell/cmd and Linux commands fill the same form', () => {
  for (const script of ['.\\config.cmd', './config.cmd', 'config.cmd', './config.sh']) {
    const result = parse(`${script} --url "https://github.com/owner/repo" --token 'TOKEN'`);
    assert.equal(result.ok, true);
    assert.equal(result.target_type, 'repo');
    assert.equal(result.target, 'owner/repo');
    assert.equal(result.registration_token, 'TOKEN');
    assert.equal(result.suggested_name, 'repo');
  }
  assert.equal(parse('.\\config.cmd --url https://github.com/company --token TOKEN').target_type, 'org');
});

test('pasted commands are data and invalid GitHub targets are rejected', () => {
  assert.equal(parse('.\\config.cmd --url https://github.com/o/r --token TOKEN & echo injected').registration_token, 'TOKEN');
  for (const url of ['http://github.com/o/r', 'https://example.com/o/r', 'https://github.com/o/r/extra', 'https://user:pass@github.com/o/r', 'https://github.com/o/r?x=1']) {
    assert.equal(parse(`.\\config.cmd --url ${url} --token TOKEN`).ok, false, url);
  }
  assert.equal(parse('').ok, false);
  assert.equal(parse('.\\config.cmd --token TOKEN').ok, false);
});
