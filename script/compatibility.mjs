// The snapshot is generated from reviewed Platform source, never a second matrix.
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync, writeFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { resolve } from 'node:path';
import * as authority from '../internal/cli/protocol/source/network-compatibility.ts';

const directory = 'internal/cli/protocol/';
const sourceCommit = '134d82457c99777cba752a549fb7e26ab239d71c';
const sourceTree = '7ff3bfca36f1e0b8b4c5d46d43e0510454d99b5c';
const paths = {
  'source/network-compatibility.ts': 'src/protocol/services/network_compatibility/index.ts',
  'review-v1.json': 'protocol/review-v1.json',
};
const digest = (bytes) => createHash('sha256').update(bytes).digest('hex');
const contract = JSON.parse(readFileSync(directory + 'review-v1.json', 'utf8'));
const capability = {
  source: { repository: 'taco3064/shoal-app', commit: sourceCommit, tree: sourceTree },
  sourceFiles: Object.fromEntries(Object.entries(paths).map(([copy, path]) => [path, digest(readFileSync(directory + copy))])),
  networkRoot: authority.networkRoot,
  requestFormPath: authority.requestFormPath,
  summaryWorkflowPath: authority.summaryWorkflowPath,
  reviewProtocolVersion: contract.protocolVersion,
  admissionMarker: contract.admission.marker,
  eventMarker: contract.event.marker,
  supportedReviewerSummaryContracts: authority.supportedReviewerSummaryContracts,
  requestFormDigests: [...authority.allowedCanonicalReviewRequestFormDigests],
  summaryWorkflows: Object.fromEntries(authority.allowedSummaryWorkflows),
};
const bytes = JSON.stringify(capability, null, 2) + '\n';
if (process.argv[2] === '--generate') {
  writeFileSync(directory + 'capability.json', bytes);
} else {
  assert.equal(readFileSync(directory + 'capability.json', 'utf8'), bytes, 'Generated capability drift');
  if (!process.argv[2]) throw new Error('usage: node script/compatibility.mjs <exact shoal-app checkout>');
  const source = resolve(process.argv[2]);
  const git = (...args) => execFileSync('git', ['-C', source, ...args], { encoding: 'utf8' }).trim();
  assert.equal(git('rev-parse', 'HEAD'), sourceCommit, 'Platform source commit mismatch');
  assert.equal(git('rev-parse', 'HEAD^{tree}'), sourceTree, 'Platform source tree mismatch');
  for (const [copy, path] of Object.entries(paths)) {
    const exact = execFileSync('git', ['-C', source, 'show', `${sourceCommit}:${path}`]);
    assert.deepEqual(readFileSync(directory + copy), exact, `Platform byte correspondence: ${path}`);
    assert.deepEqual(readFileSync(resolve(source, path)), exact, `Dirty Platform source: ${path}`);
  }
  console.log(`Compatibility correspondence PASS: ${sourceCommit} / ${sourceTree}; snapshot sha256=${digest(bytes)}`);
}
