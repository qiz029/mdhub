export const MAX_IMAGE_BYTES = 20 * 1024 * 1024;
const OPTIMIZE_ABOVE_BYTES = 5 * 1024 * 1024;
const MAX_IMAGE_DIMENSION = 2560;
const WEBP_QUALITY = 0.82;

const acceptedImageTypes = new Set([
  "image/png",
  "image/jpeg",
  "image/gif",
  "image/webp",
  "image/avif",
]);

export type UploadedImage = {
  path: string;
  href: string;
  mime: string;
  size: number;
};

export type MarkdownInsertion = {
  markdown: string;
  cursor: number;
};

function fileStem(name: string): string {
  return name.replace(/\.[^.]+$/, "") || "image";
}

export function imageAltText(name: string): string {
  return fileStem(name).replace(/[\[\]]/g, "").trim() || "image";
}

export function insertImageMarkdown(
  markdown: string,
  start: number,
  end: number,
  href: string,
  alt: string,
): MarkdownInsertion {
  const before = markdown.slice(0, start);
  const after = markdown.slice(end);
  const leadingBreak = before.length > 0 && !before.endsWith("\n") ? "\n" : "";
  const trailingBreak = after.length > 0 && !after.startsWith("\n") ? "\n" : "";
  const image = `![${alt.replace(/[\[\]]/g, "")}](<${href}>)`;
  const inserted = leadingBreak + image + trailingBreak;
  return {
    markdown: before + inserted + after,
    cursor: before.length + leadingBreak.length + image.length + trailingBreak.length,
  };
}

export async function optimizeImage(file: File): Promise<File> {
  if (file.size > MAX_IMAGE_BYTES) {
    throw new Error("图片不能超过 20 MB");
  }
  if (!acceptedImageTypes.has(file.type)) {
    throw new Error("只支持 PNG、JPEG、GIF、WebP 或 AVIF");
  }
  if (
    (file.type !== "image/png" &&
      file.type !== "image/jpeg" &&
      file.type !== "image/webp") ||
    typeof createImageBitmap === "undefined"
  ) {
    return file;
  }

  let bitmap: ImageBitmap;
  try {
    bitmap = await createImageBitmap(file);
  } catch {
    return file;
  }
  try {
    const scale = Math.min(
      1,
      MAX_IMAGE_DIMENSION / Math.max(bitmap.width, bitmap.height),
    );
    if (scale === 1 && file.size <= OPTIMIZE_ABOVE_BYTES) return file;

    const canvas = document.createElement("canvas");
    canvas.width = Math.max(1, Math.round(bitmap.width * scale));
    canvas.height = Math.max(1, Math.round(bitmap.height * scale));
    const context = canvas.getContext("2d");
    if (!context) return file;
    context.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
    const blob = await new Promise<Blob | null>((resolve) =>
      canvas.toBlob(resolve, "image/webp", WEBP_QUALITY),
    );
    if (!blob || (scale === 1 && blob.size >= file.size)) return file;
    return new File([blob], `${fileStem(file.name)}.webp`, {
      type: "image/webp",
      lastModified: file.lastModified,
    });
  } finally {
    bitmap.close();
  }
}

export async function uploadImage(file: File): Promise<UploadedImage> {
  const optimized = await optimizeImage(file);
  const body = new FormData();
  body.set("file", optimized, optimized.name);
  const response = await fetch("/mdhub/api/image", {
    method: "POST",
    body,
  });
  const result = (await response.json().catch(() => ({}))) as Partial<
    UploadedImage & { error: string }
  >;
  if (!response.ok || !result.href) {
    throw new Error(result.error || `图片上传失败（HTTP ${response.status}）`);
  }
  return result as UploadedImage;
}
