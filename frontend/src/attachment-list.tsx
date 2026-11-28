import { createContext, useContext, useEffect, useState } from "react";
import { attachmentPath, isPreviewImage, type Attachment } from "./attachments";
import { ImagePreview } from "./image-preview";
import { Icon } from "./icons";
import { isOfficeFile, OpenOfficePreview } from "./office-preview";
import { useI18n } from "./preferences";

export const ArchivedAttachments = createContext<Record<
  string,
  Record<string, string | null>
> | null>(null);

export function AttachmentList({
  attachments,
  agentId,
}: {
  attachments: Attachment[];
  agentId: string;
}) {
  const { t } = useI18n();
  const archive = useContext(ArchivedAttachments);
  const officePreview = useContext(OpenOfficePreview);
  const [preview, setPreview] = useState<{
    name: string;
    src: string;
  }>();
  const warm = officePreview?.warm;
  const officeName =
    archive === null
      ? attachments.find((attachment) => isOfficeFile(attachment.name))?.name
      : undefined;
  useEffect(() => {
    if (officeName) warm?.(officeName);
  }, [officeName, warm]);
  if (!attachments.length) return null;
  return (
    <>
      <ul
        aria-label={t("Anhänge")}
        className="mt-3 flex min-w-0 flex-wrap gap-2"
      >
        {attachments.map((attachment) => {
          const image = isPreviewImage(attachment.mimeType);
          const office = archive === null && isOfficeFile(attachment.name);
          const data = archive?.[agentId]?.[attachment.id];
          const src =
            archive === null
              ? attachmentPath(agentId, attachment.id)
              : typeof data === "string" && image
                ? `data:${attachment.mimeType};base64,${data}`
                : undefined;
          const download =
            archive === null
              ? `${attachmentPath(agentId, attachment.id)}?download=1`
              : typeof data === "string"
                ? `data:application/octet-stream;base64,${data}`
                : undefined;

          return (
            <li
              key={attachment.id}
              className="flex min-w-0 max-w-64 items-center gap-2 rounded-lg border border-border p-2"
              title={attachment.path || undefined}
            >
              {image && src ? (
                <button
                  type="button"
                  className="shrink-0 cursor-zoom-in rounded focus-visible:outline-2"
                  onClick={() => setPreview({ name: attachment.name, src })}
                  aria-label={t("Bild öffnen: {name}", {
                    name: attachment.name,
                  })}
                >
                  <img
                    src={src}
                    alt={attachment.name}
                    loading="lazy"
                    decoding="async"
                    className="size-16 rounded object-cover"
                  />
                </button>
              ) : (
                <Icon name="file" className="size-7" />
              )}
              <span className="min-w-0">
                {office && download && officePreview ? (
                  <button
                    type="button"
                    className="block max-w-full cursor-pointer truncate text-left text-sm underline"
                    onClick={() =>
                      officePreview.open({
                        name: attachment.name,
                        url: download,
                      })
                    }
                  >
                    {attachment.name}
                  </button>
                ) : download ? (
                  <a
                    className="block truncate text-sm underline"
                    href={download}
                    download={attachment.name}
                  >
                    {attachment.name}
                  </a>
                ) : (
                  <span className="block truncate text-sm">
                    {attachment.name}
                  </span>
                )}
                <span className="block text-xs text-muted">
                  {Math.ceil(attachment.size / 1024)} KiB
                </span>
                {archive && !download && (
                  <span className="block text-xs text-muted">
                    {t("Nicht im Export enthalten")}
                  </span>
                )}
                {attachment.path && (
                  <code className="block truncate text-[10px] text-muted">
                    {attachment.path}
                  </code>
                )}
              </span>
            </li>
          );
        })}
      </ul>
      {preview && (
        <ImagePreview {...preview} onClose={() => setPreview(undefined)} />
      )}
    </>
  );
}
