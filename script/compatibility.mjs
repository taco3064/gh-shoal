// The snapshot is generated from reviewed Platform source, never a second matrix.
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync, writeFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { resolve } from 'node:path';
import * as authority from '../reviewruntime/protocol/source/network-compatibility.ts';

const directory = 'reviewruntime/protocol/';
const sourceCommit = '443fba06046acaaa71f2e8c67e64a6d3af053c4f';
const sourceTree = 'ac84215752db0d2ccf6b566bc8ad49be2e66159e';
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
  evidence: contract.evidence,
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
  assert.equal(git('status', '--porcelain', '--untracked-files=all'), '', 'Platform source checkout must be clean');
  for (const [copy, path] of Object.entries(paths)) {
    const exact = execFileSync('git', ['-C', source, 'show', `${sourceCommit}:${path}`]);
    assert.deepEqual(readFileSync(directory + copy), exact, `Platform byte correspondence: ${path}`);
    assert.deepEqual(readFileSync(resolve(source, path)), exact, `Dirty Platform source: ${path}`);
  }
  console.log(`Compatibility correspondence PASS: ${sourceCommit} / ${sourceTree}; snapshot sha256=${digest(bytes)}`);
}
