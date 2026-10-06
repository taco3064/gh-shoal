import assert from 'node:assert/strict';
import { register } from 'node:module';
import { execFileSync } from 'node:child_process';
import { readFileSync, mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { resolve, join } from 'node:path';
import { pathToFileURL } from 'node:url';

// The input checkout is first identity-checked by compatibility.mjs. Compile its
// actual accepted codec rather than maintaining a second expected JSON parser.
const source = resolve(process.argv[2]);
execFileSync(process.execPath, ['script/compatibility.mjs', source], { stdio: 'inherit' });
const npm = process.platform === 'win32' ? 'npm.cmd' : 'npm';
for (const args of [['ci'], ['run', 'package:action']]) {
  execFileSync(npm, args, { cwd: source, stdio: 'inherit', shell: process.platform === 'win32' });
}
register(pathToFileURL(resolve(source, 'dist/action-package/loader.mjs')), import.meta.url);
const directory = resolve(source, 'dist/action-package/src/protocol/services/review_protocol');
const { decodeEvidenceDocument } = await import(pathToFileURL(resolve(directory, 'evidence_document.js')));
const { renderEvidenceComment } = await import(pathToFileURL(resolve(directory, 'evidence_comment.js')));
const { parseProtocolComment } = await import(pathToFileURL(resolve(directory, 'review_protocol.js')));
const temporary = mkdtempSync(join(tmpdir(), 'shoal-evidence-parity-'));
try {
  const output = join(temporary, 'comments.json');
  execFileSync('go', ['test', './reviewruntime', '-run', '^TestCanonicalEvidenceRoundTrip$', '-count=1'], {
    stdio: 'inherit', env: { ...process.env, SHOAL_EVIDENCE_PARITY_OUTPUT: output },
  });
  for (const body of JSON.parse(readFileSync(output, 'utf8'))) {
    const decoded = decodeEvidenceDocument(body);
    assert.equal(decoded.kind, 'present');
    const { record, presentation } = decoded.document;
    const parsed = parseProtocolComment(body);
    assert.ok(['admission', 'judgment', 'lifecycle'].includes(parsed.kind));
    assert.deepEqual(parsed.value, parsed.kind === 'lifecycle' ? { ...record, automationProvenance: null } : record);
    const rendered = renderEvidenceComment(record, presentation);
    assert.equal(body.split('<!-- shoal-evidence:')[0], rendered.split('<!-- shoal-evidence:')[0]);
    assert.deepEqual(decodeEvidenceDocument(rendered).document, decoded.document);
  }
  console.log('All eight runtime record types match the exact Platform parser and human renderer.');
} finally {
  rmSync(temporary, { recursive: true, force: true });
}
