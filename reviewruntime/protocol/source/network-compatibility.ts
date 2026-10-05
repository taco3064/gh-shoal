// Explicit official versions, never live trust-on-first-use discovery.
// Update only after the canonical station files and Action release have been reviewed.
export const networkRoot = {
  fullName: 'taco3064/shoal-station',
  repositoryId: 1379044983,
} as const;

export const summaryWorkflowPath = '.github/workflows/reviewer-summary.yml';
export const requestFormPath = '.github/ISSUE_TEMPLATE/review-request.yml';

export type ReviewerSummaryContract = {
  protocolVersion: number;
  summarySchemaVersion: number;
};

export type SummaryWorkflowTrust = {
  actionCommit: string;
  reviewerSummary: ReviewerSummaryContract;
};

export const legacyReviewerSummaryContract = {
  protocolVersion: 1,
  summarySchemaVersion: 1,
} as const satisfies ReviewerSummaryContract;

export const currentReviewerSummaryContract = {
  protocolVersion: 1,
  summarySchemaVersion: 2,
} as const satisfies ReviewerSummaryContract;

export const supportedReviewerSummaryContracts = [
  legacyReviewerSummaryContract,
  currentReviewerSummaryContract,
] as const satisfies readonly ReviewerSummaryContract[];

// Public Git transport: one attempt-addressable tag per Summary Attempt.
export function summaryTransportTag(
  repositoryId: number,
  runId: number,
  attempt: number,
): string {
  return `shoal-summary-${repositoryId}-${runId}-${attempt}`;
}

export const allowedCanonicalReviewRequestFormDigests = new Set([
  '6f5dad1fd33ec1ea077eb30e866ef8dcf4d9d840239701b997781418da6c7f2c',
]);

// Each trusted workflow's exact bytes bind to a pinned official Action and an
// explicit Reviewer Summary Protocol/schema contract.
export const allowedSummaryWorkflows = new Map<string, SummaryWorkflowTrust>([
  [
    // shoal-app#49 Stage A: final Phase 4 caller; auxiliary Hosted trust is separate.
    'b9162cae864bbd6e00745346f37f701fe5c003d3367cc3dc37c6fb394f9d8105',
    {
      actionCommit: '4918e1afe85f15f8fe263eaf2866cd02a1f70a62',
      reviewerSummary: { protocolVersion: 1, summarySchemaVersion: 2 },
    },
  ],
  [
    // shoal-station#13: independently accepted workload generation.
    'f6cda44c7e6e12117dac3c1f1c145bb69283eb3cfe66690ba67adddd3b4e89bd',
    {
      actionCommit: '4918e1afe85f15f8fe263eaf2866cd02a1f70a62',
      // Freeze this production binding independently of future source evolution.
      reviewerSummary: { protocolVersion: 1, summarySchemaVersion: 2 },
    },
  ],
  [
    // shoal-station#9, b93d42c0: integrity checks and deterministic recovery.
    '3b66f6c4afb545bbf1ad847aed96d0dd8c336e6df100c6b898250a0bddf58fd6',
    {
      actionCommit: 'b4d72405ebc03afc35d29093302b5593e1ddff1b',
      reviewerSummary: legacyReviewerSummaryContract,
    },
  ],
  [
    '616eea6f7ce06c0991f0023768c02c79a99d53aeb2f845c0934461720546a999',
    {
      actionCommit: 'b4d72405ebc03afc35d29093302b5593e1ddff1b',
      reviewerSummary: legacyReviewerSummaryContract,
    },
  ],
  [
    // shoal-station 1d329860: attempt-addressable public transport, same pinned Action.
    'd586ab618c894d9729e21d7105becb0ca805df576198353293b1f77818927e99',
    {
      actionCommit: 'b4d72405ebc03afc35d29093302b5593e1ddff1b',
      reviewerSummary: legacyReviewerSummaryContract,
    },
  ],
  [
    // shoal-station#11: corrected Root-owner requester support.
    '70d1011d0b1a6a68677bc891a408f2b73af868a89d283bffdfefa2fd24a6b9d2',
    {
      actionCommit: 'e1824eaa4766891a6fe56bb1ea2dfb3f13541e73',
      reviewerSummary: legacyReviewerSummaryContract,
    },
  ],
]);

export function isSupportedReviewerSummaryContract(
  candidate: ReviewerSummaryContract,
  supported: readonly ReviewerSummaryContract[] = supportedReviewerSummaryContracts,
): boolean {
  return supported.some(
    (contract) =>
      contract.protocolVersion === candidate.protocolVersion
      && contract.summarySchemaVersion === candidate.summarySchemaVersion,
  );
}
