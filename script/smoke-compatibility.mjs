// Real gh extension installation and command entry points; only GitHub/Agent
// boundaries are controlled. No personal credential or real lifecycle writes.
import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { copyFileSync, cpSync, mkdtempSync, mkdirSync, readFileSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { createHash } from 'node:crypto';
import { dirname, join, resolve, delimiter } from 'node:path';

const checkout = process.cwd();
const realGh = process.env.SHOAL_REAL_GH || execFileSync(process.platform === 'win32' ? 'where' : 'which', ['gh'], { encoding: 'utf8' }).trim().split(/\r?\n/)[0];
const realGit = execFileSync(process.platform === 'win32' ? 'where' : 'which', ['git'], { encoding: 'utf8' }).trim().split(/\r?\n/)[0];
const temporary = mkdtempSync(join(tmpdir(), 'shoal-command-smoke-'));
const shimDir = join(temporary, 'bin'); mkdirSync(shimDir);
const extension = process.platform === 'win32' ? 'gh-shoal.exe' : 'gh-shoal';
const shim = join(shimDir, process.platform === 'win32' ? 'gh.exe' : 'gh');
const env = { ...process.env, GH_CONFIG_DIR: join(temporary, 'gh-config'), XDG_DATA_HOME: join(temporary,'data'), XDG_STATE_HOME: join(temporary,'state'), GH_NO_UPDATE_NOTIFIER:'1', GH_NO_EXTENSION_UPDATE_NOTIFIER:'1', SHOAL_REAL_GIT: realGit, GH_TOKEN: 'controlled-offline-smoke-placeholder', GITHUB_TOKEN: '' };
// Windows local extensions are dispatched through Git for Windows' real sh.
// Expose usr/bin/sh.exe directly: Git's bin/sh.exe launcher prepends its own
// Git directory and shadows the controlled locator shim. Normalize the
// case-insensitive PATH key for Node child processes.
const inheritedPath = Object.entries(env).find(([key]) => key.toUpperCase() === 'PATH')?.[1] || '';
for (const key of Object.keys(env)) if (key.toUpperCase() === 'PATH') delete env[key];
env.PATH = process.platform === 'win32' ? resolve(dirname(realGit), '..', 'usr', 'bin') + delimiter + inheritedPath : inheritedPath;
const run = (program, args, cwd = checkout, extra = {}) => execFileSync(program, args, { cwd, env: { ...env, ...extra }, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] });
const git = (cwd, ...args) => run('git', args, cwd).trim();
if (process.env.SHOAL_SMOKE_BINARY) copyFileSync(resolve(process.env.SHOAL_SMOKE_BINARY), extension);
else run('go', ['build', '-o', extension, './cmd/gh-shoal']);
run('go', ['build', '-o', shim, './script/smoke-gh']);
copyFileSync(shim, join(shimDir, process.platform === 'win32' ? 'codex.exe' : 'codex'));
copyFileSync(shim, join(shimDir, process.platform === 'win32' ? 'git.exe' : 'git'));
run(realGh, ['extension', 'install', '.']);
assert.match(run(realGh, ['shoal', '--help']), /re-review/);

// Historical-allowlist simulation with the current evidence codec. These are
// controlled compatibility fixtures, not a claim to execute historical binaries.
const oldCapability = readFileSync('reviewruntime/testdata/capability-v0.6.0.json');
assert.equal(createHash('sha256').update(oldCapability).digest('hex'), '85cdccabd9ffca8e7e758ebdb3d75f4abb240d86f3f67ad65265b7484a7a930d');
const oldSource = join(temporary, 'old-source', 'gh-shoal'); mkdirSync(oldSource, { recursive: true });
for (const path of ['cmd', 'internal', 'reviewruntime']) cpSync(join(checkout, path), join(oldSource, path), { recursive: true });
copyFileSync('go.mod', join(oldSource, 'go.mod'));
const historicalWithCurrentCodec = bytes => {
  const value = JSON.parse(bytes);
  value.sourceFiles['protocol/review-v1.json'] = createHash('sha256').update(readFileSync('reviewruntime/protocol/review-v1.json')).digest('hex');
  return JSON.stringify(value);
};
writeFileSync(join(oldSource, 'reviewruntime/protocol/capability.json'), historicalWithCurrentCodec(oldCapability));
run('go', ['build', '-o', extension, './cmd/gh-shoal'], oldSource);
const oldEnv = { GH_CONFIG_DIR: join(temporary, 'old-gh-config'), XDG_DATA_HOME: join(temporary, 'old-data'), XDG_STATE_HOME: join(temporary, 'old-state') };
run(realGh, ['extension', 'install', '.'], oldSource, oldEnv);

// v0.9.0 was published before final caller admission. Preserve its exact bytes
// as a refusal control; published preliminary entries are not Platform authority.
const publishedCapability = readFileSync('reviewruntime/testdata/capability-v0.9.0.json');
assert.equal(createHash('sha256').update(publishedCapability).digest('hex'), '28ccae7b338b2a9e3c64141d1231d652dacbc3ead19d29a2397d1c87ed58629c');
const publishedSource = join(temporary, 'published-source', 'gh-shoal');
cpSync(oldSource, publishedSource, { recursive: true });
writeFileSync(join(publishedSource, 'reviewruntime/protocol/capability.json'), historicalWithCurrentCodec(publishedCapability));
run('go', ['build', '-o', extension, './cmd/gh-shoal'], publishedSource);
const publishedEnv = { GH_CONFIG_DIR: join(temporary, 'published-gh-config'), XDG_DATA_HOME: join(temporary, 'published-data'), XDG_STATE_HOME: join(temporary, 'published-state') };
run(realGh, ['extension', 'install', '.'], publishedSource, publishedEnv);
const finalCaller = readFileSync('reviewruntime/testdata/reviewer-summary-final-hosted.yml', 'utf8');
assert.equal(createHash('sha256').update(finalCaller).digest('hex'), 'b9162cae864bbd6e00745346f37f701fe5c003d3367cc3dc37c6fb394f9d8105');

const form = readFileSync('reviewruntime/testdata/review-request.yml', 'utf8');
const current = readFileSync('reviewruntime/testdata/reviewer-summary-f01.yml', 'utf8');
const older = readFileSync('reviewruntime/testdata/reviewer-summary-older.yml', 'utf8');
const requestPath = '.github/ISSUE_TEMPLATE/review-request.yml';
const summaryPath = '.github/workflows/reviewer-summary.yml';
const hostedPath = '.github/workflows/hosted-review.yml';
const hosted = 'name: Controlled Hosted fixture\non:\n  workflow_call:\n';
const files = (workflow = current) => ({ [requestPath]: form, [summaryPath]: workflow, [hostedPath]: hosted });
const sha = '1'.repeat(40);
const policy = 'Reviewer-owned modified policy: never replace me.\n';
let passed = 0;
function fixture(name, localWorkflow = current) {
  const directory = join(temporary, name); mkdirSync(directory);
  const remote = join(temporary, name + '-remote.git');
  git(temporary, 'init', '--bare', remote);
  git(directory, 'init', '-b', 'main');
  git(directory, 'config', 'user.name', 'Smoke Reviewer'); git(directory, 'config', 'user.email', 'smoke@example.com');
  for (const [path, content] of Object.entries(files(localWorkflow))) {
    mkdirSync(dirname(join(directory, path)), { recursive: true }); writeFileSync(join(directory, path), content);
  }
  writeFileSync(join(directory, 'README.md'), policy); writeFileSync(join(directory, '.gitignore'), '.shoal/\n');
  git(directory, 'add', '.'); git(directory, 'commit', '-m', 'Controlled fork');
  git(directory, 'remote', 'add', 'origin', 'https://github.com/reviewer/shoal-station.git');
  // Native git transport uses a local bare repository while the authoritative
  // GitHub locator remains the validated Personal Account direct-fork locator.
  git(directory, 'config', `url.${remote.replaceAll('\\', '/')}.insteadOf`, 'https://github.com/reviewer/shoal-station.git');
  git(directory, 'push', 'origin', 'HEAD:refs/heads/main');
  const statePath = join(temporary, name + '.json');
  const node = { id: 11, full_name: 'reviewer/shoal-station', default_branch: 'main', fork: true, has_issues: true, owner: { id: 1, login: 'reviewer', type: 'User' }, parent: { id: 1379044983 } };
  const state = { node, root: { id: 1379044983, full_name: 'root/shoal-station', default_branch: 'main', owner: { id: 4, login: 'root', type: 'User' } }, managed: files(localWorkflow), canonical: files(), issues: [], comments: {}, writes: 0, agentCalls: 0, starred: false, workflowActive: true };
  const save = () => writeFileSync(statePath, JSON.stringify(state));
  const observed = () => JSON.parse(readFileSync(statePath, 'utf8'));
  function command(name, wanted = 0, reason, installEnv = {}) {
    save();
    const result = spawnSync(realGh, ['shoal', name, ...(name === 'init' ? [] : ['--agent', 'codex'])], { cwd: directory, env: { ...env, ...installEnv, PATH: shimDir + delimiter + env.PATH, SHOAL_SMOKE_STATE: statePath }, encoding: 'utf8' });
    assert.equal(result.status, wanted, `${name}: ${result.stdout}\n${result.stderr}`);
    if (reason) assert.match(result.stdout + result.stderr, new RegExp(reason));
    Object.assign(state, observed());
    assert.equal(readFileSync(join(directory, 'README.md'), 'utf8'), policy, 'Reviewer-owned README changed');
    return result;
  }
  return { directory, state, command };
}
const test = (name, body) => { body(); passed++; console.log(`PASS ${name}`); };
const request = () => ({ number: 1, state: 'open', body: '### Repository name\n\nproject\n', user: { id: 2, login: 'alice', type: 'User' } });
const evidence = record => '<!-- shoal-evidence:v1:start -->\n' + JSON.stringify({formatVersion:1,record,presentation:{}}) + '\n<!-- shoal-evidence:v1:end -->';
const admission = () => ({ id: 10, user: { id: 1 }, body: evidence({ reviewerNodeId: 11, targetRepositoryId: 55, repositoryName: 'project' }) });
const judgment = () => ({ id: 11, user: { id: 1 }, body: evidence({ type: 'REVIEWED', reviewerNodeId: 11, targetRepositoryId: 55, targetRepositoryFullName: 'alice/project', targetDefaultBranch: 'main', targetCommit: sha, reviewPolicyPath: 'README.md', reviewPolicyCommit: sha, verdict: 'PASS', actualStarState: true, reviewedAt: '2026-09-24T00:00:00Z' }) });
try {
  test('historical published allowlist refuses final caller before Platform refresh', () => {
    const f = fixture('published-final', finalCaller); f.state.canonical = files(finalCaller); f.state.issues = [request()];
    const pre = git(f.directory, 'rev-parse', 'HEAD');
    for (const name of ['init', 'review', 're-review']) f.command(name, 1, 'CLI_UPGRADE_REQUIRED', publishedEnv);
    assert.equal(f.state.writes, 0); assert.equal(f.state.agentCalls, 0);
    assert.equal(f.state.issues[0].state, 'open'); assert.deepEqual(f.state.comments, {}); assert.equal(f.state.starred, false);
    assert.equal(git(f.directory, 'rev-parse', 'HEAD'), pre); assert.equal(git(f.directory, 'status', '--porcelain'), '');
    f.command('init', 0, 'NO_CHANGES');
    f.command('review', 0, 'SUPPORTED');
    assert.equal(f.state.agentCalls, 1); assert.equal(f.state.starred, true); assert.equal(f.state.issues[0].state, 'closed');
    f.command('re-review', 0, 'SUPPORTED');
    assert.equal(f.state.agentCalls, 1); assert.equal(f.state.comments['1'].length, 2);
  });
  test('one-byte final caller drift refuses semantic work before any mutation', () => {
    const f = fixture('final-drift', finalCaller + ' '); f.state.canonical = files(finalCaller); f.state.issues = [request()];
    for (const name of ['review', 're-review']) f.command(name, 1, 'REPAIRABLE_STATION_DRIFT');
    assert.equal(f.state.writes, 0); assert.equal(f.state.agentCalls, 0); assert.equal(f.state.starred, false);
    assert.equal(f.state.issues[0].state, 'open'); assert.deepEqual(f.state.comments, {});
  });
  test('historical allowlist refuses newer init, Review and Re-review with zero mutation', () => {
    const f = fixture('old-capability'); f.state.issues = [request()];
    const pre = git(f.directory, 'rev-parse', 'HEAD');
    for (const name of ['init', 'review', 're-review']) f.command(name, 1, 'CLI_UPGRADE_REQUIRED', oldEnv);
    assert.equal(f.state.writes, 0); assert.equal(f.state.agentCalls, 0);
    assert.equal(f.state.issues[0].state, 'open'); assert.deepEqual(f.state.comments, {}); assert.equal(f.state.starred, false);
    assert.equal(git(f.directory, 'rev-parse', 'HEAD'), pre); assert.equal(git(f.directory, 'status', '--porcelain'), '');
    // The same station is admitted solely by the refreshed embedded snapshot.
    f.state.issues = [];
    for (const name of ['init', 'review', 're-review']) f.command(name, 0, name === 'init' ? 'NO_CHANGES' : 'SUPPORTED');
    assert.equal(f.state.writes, 0); assert.equal(f.state.agentCalls, 0); assert.equal(git(f.directory, 'rev-parse', 'HEAD'), pre);
  });
  test('Phase 3 re-review accepts an already-complete thread without mutation', () => {
    const f = fixture('phase3-rereview'); f.state.issues = [{ ...request(), state: 'closed' }];
    f.state.comments['1'] = [admission(), judgment()]; f.state.starred = true;
    f.command('re-review', 0, 'SUPPORTED');
    assert.equal(f.state.writes, 0); assert.equal(f.state.agentCalls, 0); assert.equal(f.state.comments['1'].length, 2);
  });
  test('current supported generation executes real Review lifecycle', () => {
    const f = fixture('current'); f.state.issues = [request()];
    f.command('review', 0, 'SUPPORTED');
    assert.equal(f.state.agentCalls, 1); assert.equal(f.state.starred, true);
    assert.equal(f.state.comments['1'].length, 2); assert.equal(f.state.issues[0].state, 'closed');
  });
  test('older supported generation allows review and re-review, explicit init upgrades', () => {
    const f = fixture('older', older); f.command('review', 0, 'NO_CHANGES');
    f.state.issues = [{ ...request(), state: 'closed' }]; f.state.comments['1'] = [admission(), judgment()]; f.state.starred = true;
    f.command('re-review', 0, 'NO_CHANGES');
    const pre = git(f.directory, 'rev-parse', 'HEAD');
    f.command('init', 0, 'SUPPORTED');
    assert.notEqual(git(f.directory, 'rev-parse', 'HEAD'), pre);
    assert.equal(readFileSync(join(f.directory, summaryPath), 'utf8'), current);
    assert.equal(f.state.agentCalls, 0);
  });
  test('new unsupported canonical generation refuses init with zero mutation', () => {
    const f = fixture('newer'); f.state.canonical = files(current + '\n'); f.state.node.has_issues = false; f.state.workflowActive = false;
    const pre = git(f.directory, 'rev-parse', 'HEAD'); f.command('init', 1, 'CLI_UPGRADE_REQUIRED');
    assert.equal(f.state.writes, 0); assert.equal(git(f.directory, 'rev-parse', 'HEAD'), pre); assert.equal(git(f.directory, 'status', '--porcelain'), '');
  });
  test('byte drift refuses Review and re-review, canonical init repairs', () => {
    const f = fixture('drift', current + '\n'); f.state.issues = [request()];
    f.command('review', 1, 'REPAIRABLE_STATION_DRIFT'); f.command('re-review', 1, 'REPAIRABLE_STATION_DRIFT');
    assert.equal(f.state.writes, 0); assert.equal(f.state.agentCalls, 0);
    f.command('init'); assert.equal(readFileSync(join(f.directory, summaryPath), 'utf8'), current);
  });
  test('unsupported formal Protocol evidence refuses both commands before side effects', () => {
    const f = fixture('history'); f.state.issues = [request()];
    f.state.comments['1'] = [{ id: 10, user: { id: 1 }, body: '<!-- shoal-evidence:v999:start -->\n{}\n<!-- shoal-evidence:v999:end -->' }];
    f.command('review', 1, 'INCOMPATIBLE_PROTOCOL_EVIDENCE'); f.command('re-review', 1, 'INCOMPATIBLE_PROTOCOL_EVIDENCE');
    assert.equal(f.state.writes, 0); assert.equal(f.state.agentCalls, 0);
  });
  test('unavailable canonical reads refuse all commands without guessed support', () => {
    const f = fixture('unavailable'); f.state.unavailable = '/contents/';
    for (const name of ['init', 'review', 're-review']) f.command(name, 1, 'EXTERNAL_STATE_UNAVAILABLE');
    assert.equal(f.state.writes, 0); assert.equal(f.state.agentCalls, 0);
  });
  test('already converged init has no synthetic commit push or GitHub mutation', () => {
    const f = fixture('noop'); const pre = git(f.directory, 'rev-parse', 'HEAD');
    f.command('init', 0, 'NO_CHANGES'); f.command('init', 0, 'NO_CHANGES');
    assert.equal(git(f.directory, 'rev-parse', 'HEAD'), pre); assert.equal(f.state.writes, 0);
  });
  test('init migrates missing and stale Hosted reusable bytes through the installed extension', () => {
    for (const initial of ['missing', 'stale']) {
      const f = fixture('hosted-' + initial);
      if (initial === 'missing') {
        git(f.directory, 'rm', hostedPath); delete f.state.managed[hostedPath];
      } else {
        writeFileSync(join(f.directory, hostedPath), 'stale reusable bytes\n');
        f.state.managed[hostedPath] = 'stale reusable bytes\n'; git(f.directory, 'add', hostedPath);
      }
      git(f.directory, 'commit', '-m', 'Controlled auxiliary drift'); git(f.directory, 'push', 'origin', 'HEAD:refs/heads/main');
      const pre = git(f.directory, 'rev-parse', 'HEAD');
      f.command('init', 0, 'SUPPORTED');
      const migrated = git(f.directory, 'rev-parse', 'HEAD');
      assert.notEqual(migrated, pre); assert.equal(readFileSync(join(f.directory, hostedPath), 'utf8'), hosted);
      assert.equal(git(f.directory, 'ls-remote', 'origin', 'refs/heads/main').slice(0, 40), migrated);
      f.state.managed[hostedPath] = hosted;
      f.command('init', 0, 'NO_CHANGES');
      assert.equal(git(f.directory, 'rev-parse', 'HEAD'), migrated); assert.equal(git(f.directory, 'status', '--porcelain'), '');
      assert.equal(f.state.writes, 0); assert.equal(f.state.agentCalls, 0);
    }
  });
  test('supported legacy Root preserves two-file init convergence and existing auxiliary bytes', () => {
    const legacyCaller = readFileSync('reviewruntime/testdata/reviewer-summary-current.yml', 'utf8');
    const f = fixture('legacy-root', legacyCaller); f.state.canonical = files(legacyCaller); delete f.state.canonical[hostedPath];
    const pre = git(f.directory, 'rev-parse', 'HEAD');
    f.command('init', 0, 'NO_CHANGES');
    assert.equal(git(f.directory, 'rev-parse', 'HEAD'), pre);
    assert.equal(readFileSync(join(f.directory, hostedPath), 'utf8'), hosted); assert.equal(f.state.writes, 0);
  });
  test('Review and Re-review never read unavailable auxiliary Hosted capability', () => {
    for (const auxiliary of ['missing', 'stale', 'unavailable']) {
      const f = fixture('optional-' + auxiliary);
      if (auxiliary === 'missing') { delete f.state.managed[hostedPath]; delete f.state.canonical[hostedPath]; }
      if (auxiliary === 'stale') { f.state.managed[hostedPath] = 'unusable auxiliary bytes'; f.state.canonical[hostedPath] = 'unusable auxiliary bytes'; }
      if (auxiliary === 'unavailable') f.state.unavailable = hostedPath;
      f.state.workflowActive = false;
      for (const name of ['review', 're-review']) f.command(name, 0, 'NO_CHANGES');
      assert.equal(f.state.writes, 0); assert.equal(f.state.agentCalls, 0);
    }
  });
  test('init fails closed for an unavailable auxiliary or a caller whose required callee is missing', () => {
    for (const reason of ['unavailable', 'missing-required']) {
      const f = fixture('callee-' + reason);
      if (reason === 'unavailable') f.state.unavailable = hostedPath;
      else { delete f.state.canonical[hostedPath]; f.state.canonical[summaryPath] += '\n    uses: ./.github/workflows/hosted-review.yml\n'; }
      const pre = git(f.directory, 'rev-parse', 'HEAD');
      f.command('init', 1, 'EXTERNAL_STATE_UNAVAILABLE');
      assert.equal(git(f.directory, 'rev-parse', 'HEAD'), pre); assert.equal(git(f.directory, 'status', '--porcelain'), '');
      assert.equal(f.state.writes, 0); assert.equal(f.state.agentCalls, 0);
    }
  });
  test('partial prior judgment and ambiguous close recover without another judgment', () => {
    const f = fixture('partial'); f.state.issues = [request()]; f.state.comments['1'] = [admission(), judgment()]; f.state.starred = true; f.state.lost = 'state=closed';
    f.command('review'); f.command('review', 0, 'NO_CHANGES');
    assert.equal(f.state.comments['1'].length, 2); assert.equal(f.state.agentCalls, 0); assert.equal(f.state.writes, 1); assert.equal(f.state.lostDone, true);
  });
  test('ambiguous Judgment append recovers exact evidence and never repeats Agent', () => {
    const f = fixture('ambiguous'); f.state.issues = [request()]; f.state.lost = 'body=## Review Result:';
    f.command('review'); f.command('review', 0, 'NO_CHANGES');
    assert.equal(f.state.agentCalls, 1); assert.equal(f.state.comments['1'].length, 2); assert.equal(f.state.lostDone, true);
  });
  test('Issues and workflow enable recover lost acknowledgement with read-before-retry', () => {
    for (const kind of ['has_issues=true', '/enable']) {
      const f = fixture('enable-' + (kind === '/enable' ? 'workflow' : 'issues')); f.state.node.has_issues = false; f.state.workflowActive = false; f.state.lost = kind;
      f.command('init'); f.command('init', 0, 'NO_CHANGES'); assert.equal(f.state.writes, 2); assert.equal(f.state.lostDone, true);
    }
  });
  console.log(`Installed-extension compatibility smoke: ${passed} cases PASS`);
} finally { rmSync(temporary, { recursive: true, force: true }); }
