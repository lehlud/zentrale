import { Fragment, lazy, Suspense, type ReactNode } from "react";

export type SyntaxToken = { text: string; className?: string };
export type DiffSourceLine = { kind: string; text: string };

const HighlightedSource = lazy(() =>
  import("./syntax-highlight").then((module) => ({
    default: module.HighlightedSource,
  })),
);
const HighlightedDiff = lazy(() =>
  import("./syntax-highlight").then((module) => ({
    default: module.HighlightedDiff,
  })),
);

export function SyntaxTokens({ tokens }: { tokens: SyntaxToken[] }) {
  return tokens.map((token, index) =>
    token.className ? (
      <span key={index} className={token.className}>
        {token.text}
      </span>
    ) : (
      <Fragment key={index}>{token.text}</Fragment>
    ),
  );
}

// Code remains readable while the optional highlighter loads.
export function SourceCode({
  text,
  language,
}: {
  text: string;
  language: string;
}) {
  return (
    <Suspense fallback={text}>
      <HighlightedSource text={text} language={language} />
    </Suspense>
  );
}

export function DiffSyntax({
  lines,
  language,
  children,
}: {
  lines: DiffSourceLine[];
  language: string;
  children: (tokens?: SyntaxToken[][]) => ReactNode;
}) {
  return (
    <Suspense fallback={children()}>
      <HighlightedDiff lines={lines} language={language}>
        {children}
      </HighlightedDiff>
    </Suspense>
  );
}
