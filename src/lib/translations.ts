export type PaperSource = {
  input: string;
  kind: "arxiv" | "doi" | "pdf" | "web";
  identifier?: string;
  version?: string;
  canonical_url: string;
  artifact_url: string;
  title?: string;
};

export type TranslationSourceCapture = {
  capture_id: string;
  source: PaperSource;
  status: "captured" | "needs_input";
  size_bytes: number;
  existing_job_id?: string;
  existing_output_slug?: string;
  previous_job_id?: string;
  revision_conflict?: boolean;
};

export type TranslationValidation = {
  complete: boolean;
  source_chunks: number;
  translated_chunks: number;
  issues: string[];
  artifact_hash?: string;
  manifest_hash?: string;
  final_chunk_hash?: string;
  profile?: string;
  provider?: string;
  model?: string;
  source_url?: string;
  source_version?: string;
  chunk_provenance?: Array<{ ordinal: number; provider: string; model: string }>;
};

export type TranslationSourceManifest = {
  artifact_hash: string;
  chunk_count: number;
  manifest_hash: string;
  final_chunk_hash: string;
};

export type TranslationChunk = {
  ordinal: number;
  source_text: string;
  source_hash: string;
  translated_text: string;
  state: string;
  attempts: number;
  provider?: string;
  model?: string;
};

export type TranslationJob = {
  id: string;
  source: PaperSource;
  target_language: string;
  profile: string;
  state: string;
  stage: string;
  progress_current: number;
  progress_total: number;
  output_slug?: string;
  provider?: string;
  model?: string;
  source_hash?: string;
  source_manifest?: TranslationSourceManifest;
  validation?: TranslationValidation;
  error?: string;
  created_at: number;
  updated_at: number;
};

export type TranslationJobDetail = TranslationJob & {
  chunks: TranslationChunk[];
};

const TERMINAL_PROGRESS = new Set(["draft_ready", "published"]);

export function translationProgress(
  job: Pick<
    TranslationJob,
    "state" | "progress_current" | "progress_total"
  >,
): number {
  if (TERMINAL_PROGRESS.has(job.state)) return 100;
  if (job.progress_total <= 0) return 0;
  return Math.max(
    0,
    Math.min(100, Math.round((job.progress_current / job.progress_total) * 100)),
  );
}

export function translationViewHref(slug: string): string {
  return (
    "/view/" +
    slug
      .split("/")
      .map((segment) => encodeURIComponent(segment))
      .join("/")
  );
}

export const TRANSLATION_STAGE_LABELS: Record<string, string> = {
  queued: "等待 Agent",
  claimed: "Agent 已领取",
  fetching: "获取论文",
  extracting: "提取全文",
  translating: "完整翻译",
  validating: "完整性校验",
  draft_ready: "译稿待确认",
  published: "已发布",
  needs_input: "需要 PDF",
  failed: "执行失败",
  cancelled: "已取消",
};

export function translationStageLabel(stage: string): string {
  return TRANSLATION_STAGE_LABELS[stage] ?? stage;
}
