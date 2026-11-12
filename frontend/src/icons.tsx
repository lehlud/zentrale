import {
  PanelLeft,
  MessageCircle,
  SquarePen,
  Folder,
  Server,
  KeyRound,
  Plug,
  SlidersHorizontal,
  LogOut,
  Ellipsis,
  Plus,
  Copy,
  ArrowRight,
  RotateCw,
  CodeXml,
  Info,
  ArrowDown,
  ChevronDown,
  Check,
  CircleX,
  CircleDot,
  Square,
  Terminal,
  Paperclip,
  File,
  type LucideProps,
} from "lucide-react";

const icons = {
  sidebar: PanelLeft,
  chat: MessageCircle,
  compose: SquarePen,
  projects: Folder,
  machines: Server,
  keys: KeyRound,
  providers: Plug,
  settings: SlidersHorizontal,
  logout: LogOut,
  more: Ellipsis,
  plus: Plus,
  copy: Copy,
  arrow: ArrowRight,
  refresh: RotateCw,
  code: CodeXml,
  info: Info,
  down: ArrowDown,
  chevron: ChevronDown,
  check: Check,
  error: CircleX,
  working: CircleDot,
  stop: Square,
  terminal: Terminal,
  attachment: Paperclip,
  file: File,
};

export function Icon({
  name,
  className = "size-[18px]",
  ...props
}: LucideProps & { name: keyof typeof icons }) {
  const Component = icons[name];
  return (
    <Component
      aria-hidden="true"
      strokeWidth={1.75}
      className={`shrink-0 ${className}`}
      {...props}
    />
  );
}
