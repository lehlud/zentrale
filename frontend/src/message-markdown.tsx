import { Link } from "@heroui/react";
import { isValidElement, type ReactNode } from "react";
import Markdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";

import { useI18n } from "./preferences";
import { CodeSnippet } from "./code-snippet";

function CodeBlock({ children }: { children?: ReactNode }) {
  const { t } = useI18n();
  const code = isValidElement<{ children?: ReactNode; className?: string }>(
    children,
  )
    ? children.props
    : undefined;
  const text = String(code?.children ?? "").replace(/\n$/, "");
  const language = code?.className?.replace("language-", "") || t("Code");

  return <CodeSnippet text={text} label={language} />;
}

const components: Components = {
  h1: ({ children }) => (
    <h3 className="pt-2 text-lg font-medium">{children}</h3>
  ),
  h2: ({ children }) => (
    <h3 className="pt-2 text-base font-medium">{children}</h3>
  ),
  h3: ({ children }) => <h4 className="pt-2 font-medium">{children}</h4>,
  h4: ({ children }) => <h4 className="font-medium">{children}</h4>,
  p: ({ children }) => <p className="whitespace-pre-wrap">{children}</p>,
  ul: ({ children }) => (
    <ul className="list-disc space-y-1 pl-5">{children}</ul>
  ),
  ol: ({ children }) => (
    <ol className="list-decimal space-y-1 pl-5">{children}</ol>
  ),
  blockquote: ({ children }) => (
    <blockquote className="border-l-2 border-border pl-4 text-muted">
      {children}
    </blockquote>
  ),
  pre: CodeBlock,
  code: ({ children, className }) => (
    <code
      className={
        className || "rounded bg-default px-1 py-0.5 font-mono text-[0.9em]"
      }
    >
      {children}
    </code>
  ),
  a: ({ href, children }) => (
    <Link
      href={href}
      target="_blank"
      rel="noopener noreferrer"
      className="inline break-all text-sm underline underline-offset-4"
    >
      {children}
    </Link>
  ),
  img: function MarkdownImage({ alt, src }) {
    const { t } = useI18n();
    return (
      <Link href={src} target="_blank" rel="noopener noreferrer">
        {alt || t("Bild öffnen")}
      </Link>
    );
  },
  table: function MarkdownTable({ children }) {
    const { t } = useI18n();
    return (
      <div
        role="region"
        aria-label={t("Tabelle aus der Agentenantwort")}
        tabIndex={0}
        className="overflow-x-auto rounded-lg border border-border outline-none focus-visible:ring-2 focus-visible:ring-focus"
      >
        <table className="w-full text-left text-sm">{children}</table>
      </div>
    );
  },
  th: ({ children }) => (
    <th className="border-b border-border bg-default px-3 py-2 font-medium">
      {children}
    </th>
  ),
  td: ({ children }) => (
    <td className="border-b border-border px-3 py-2 align-top">{children}</td>
  ),
};

export function MessageMarkdown({ content }: { content: string }) {
  return (
    <div className="min-w-0 space-y-3 break-words text-sm leading-7 [overflow-wrap:anywhere]">
      <Markdown remarkPlugins={[remarkGfm]} components={components}>
        {content}
      </Markdown>
    </div>
  );
}
