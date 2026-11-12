import { attachmentPath, isPreviewImage, type Attachment } from "./attachments";
import { Icon } from "./icons";
import { useI18n } from "./preferences";

export function AttachmentList({
  attachments,
  agentId,
}: {
  attachments: Attachment[];
  agentId: string;
}) {
  const { t } = useI18n();
  if (!attachments.length) return null;
  return (
    <ul aria-label={t("Anhänge")} className="mt-3 flex min-w-0 flex-wrap gap-2">
      {attachments.map((attachment) => (
        <li
          key={attachment.id}
          className="flex min-w-0 max-w-64 items-center gap-2 rounded-lg border border-border p-2"
          title={attachment.path}
        >
          {isPreviewImage(attachment.mimeType) ? (
            <a
              href={attachmentPath(agentId, attachment.id)}
              target="_blank"
              rel="noopener noreferrer"
              aria-label={t("Bild öffnen: {name}", { name: attachment.name })}
            >
              <img
                src={attachmentPath(agentId, attachment.id)}
                alt={attachment.name}
                loading="lazy"
                decoding="async"
                className="size-16 rounded object-cover"
              />
            </a>
          ) : (
            <Icon name="file" className="size-7" />
          )}
          <span className="min-w-0">
            <span className="block truncate text-sm">{attachment.name}</span>
            <span className="block text-xs text-muted">
              {Math.ceil(attachment.size / 1024)} KiB
            </span>
            <code className="block truncate text-[10px] text-muted">
              {attachment.path}
            </code>
          </span>
        </li>
      ))}
    </ul>
  );
}
