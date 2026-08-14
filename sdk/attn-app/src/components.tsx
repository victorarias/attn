// The component slice: what a view needs to stop looking foreign.
//
// Tokens come for free — a view mounts inside attn's DOM, so every
// `var(--color-*)` and `var(--font-*)` already resolves — and that is most of
// "looks native" at zero cost. What is left is the handful of controls a view
// would otherwise hand-roll: attn has 55 distinct button class names across 51
// stylesheets, which is the receipt for why `Button` is component number one.
//
// These render class names and nothing else. The stylesheet backing them lives
// in attn's own build (app/src/components/appViews/appSdkComponents.css), not
// here: the SDK is a rollup entry the import map resolves to, so a CSS import in
// this file would emit an asset no page links and every style would silently
// vanish. app/src/components/appViews/appSdkComponents.classes.test.tsx renders
// each component and fails on a class the stylesheet does not define.
//
// Two components are deliberately absent, and stay absent until a view needs
// them: a spinner (a permanently animating element is a battery bug in a window
// that is open all day — loading states are text) and a relative-time label (a
// repaint loop waiting to happen; an absolute timestamp with a `title` costs
// nothing and repaints never).
//
// Design: docs/plans/2026-08-13-ext-a5-ui-host-and-app-sdk.md, "The
// design-system slice".

import type { ChangeEvent, KeyboardEvent, ReactElement, ReactNode } from "react"
import ReactMarkdown from "react-markdown"
import remarkGfm from "remark-gfm"

function classes(...names: Array<string | false | null | undefined>): string {
  return names.filter(Boolean).join(" ")
}

/**
 * `primary` is the one action a view wants; `secondary` is everything else;
 * `danger` is the one that destroys something. There is no fourth: a view that
 * needs another meaning is telling the SDK about a component it is missing.
 */
export type ButtonVariant = "primary" | "secondary" | "danger"

export interface ButtonProps {
  children?: ReactNode
  variant?: ButtonVariant
  disabled?: boolean
  onClick?: () => void
  /** Buttons in a view are actions, never form submissions, so this defaults to "button". */
  type?: "button" | "submit"
  title?: string
  className?: string
}

export function Button({
  children,
  variant = "secondary",
  disabled,
  onClick,
  type = "button",
  title,
  className,
}: ButtonProps): ReactElement {
  return (
    <button
      type={type}
      className={classes("attn-app-button", `attn-app-button-${variant}`, className)}
      disabled={disabled}
      onClick={onClick}
      title={title}
    >
      {children}
    </button>
  )
}

export interface TextInputProps {
  value: string
  onChange: (value: string) => void
  placeholder?: string
  disabled?: boolean
  /** Rendered under the field, in the error colour. A refusal the user can read. */
  error?: string
  ariaLabel?: string
  className?: string
}

export function TextInput({
  value,
  onChange,
  placeholder,
  disabled,
  error,
  ariaLabel,
  className,
}: TextInputProps): ReactElement {
  return (
    <div className={classes("attn-app-field", className)}>
      <input
        type="text"
        className={classes("attn-app-input", error && "attn-app-input-invalid")}
        value={value}
        placeholder={placeholder}
        disabled={disabled}
        aria-label={ariaLabel}
        aria-invalid={error ? true : undefined}
        onChange={(event: ChangeEvent<HTMLInputElement>) => onChange(event.target.value)}
      />
      {error && <div className="attn-app-field-error">{error}</div>}
    </div>
  )
}

export interface TextAreaProps extends TextInputProps {
  /** Visible rows. The box does not grow on its own — a view that wants that says so. */
  rows?: number
}

export function TextArea({
  value,
  onChange,
  placeholder,
  disabled,
  error,
  ariaLabel,
  className,
  rows = 3,
}: TextAreaProps): ReactElement {
  return (
    <div className={classes("attn-app-field", className)}>
      <textarea
        className={classes("attn-app-textarea", error && "attn-app-input-invalid")}
        value={value}
        rows={rows}
        placeholder={placeholder}
        disabled={disabled}
        aria-label={ariaLabel}
        aria-invalid={error ? true : undefined}
        onChange={(event: ChangeEvent<HTMLTextAreaElement>) => onChange(event.target.value)}
      />
      {error && <div className="attn-app-field-error">{error}</div>}
    </div>
  )
}

export interface ListProps {
  children?: ReactNode
  className?: string
}

/** The scrolling column a view's rows live in. */
export function List({ children, className }: ListProps): ReactElement {
  return (
    <div className={classes("attn-app-list", className)} role="list">
      {children}
    </div>
  )
}

export interface ListRowProps {
  /** The line the user reads first. */
  title: ReactNode
  /** The quieter second line: who, when, which session. */
  meta?: ReactNode
  /** Trailing controls, laid out at the row's end. */
  actions?: ReactNode
  /** A row that answers a click is focusable and reachable by keyboard. */
  onClick?: () => void
  selected?: boolean
  className?: string
}

export function ListRow({
  title,
  meta,
  actions,
  onClick,
  selected,
  className,
}: ListRowProps): ReactElement {
  const interactive = !!onClick
  return (
    <div
      className={classes(
        "attn-app-list-row",
        interactive && "attn-app-list-row-interactive",
        selected && "attn-app-list-row-selected",
        className,
      )}
      role={interactive ? "button" : "listitem"}
      tabIndex={interactive ? 0 : undefined}
      aria-selected={selected}
      onClick={onClick}
      onKeyDown={
        interactive
          ? (event: KeyboardEvent<HTMLDivElement>) => {
              if (event.key === "Enter" || event.key === " ") {
                event.preventDefault()
                onClick?.()
              }
            }
          : undefined
      }
    >
      <div className="attn-app-list-row-body">
        <div className="attn-app-list-row-title">{title}</div>
        {meta && <div className="attn-app-list-row-meta">{meta}</div>}
      </div>
      {actions && <div className="attn-app-list-row-actions">{actions}</div>}
    </div>
  )
}

export interface EmptyStateProps {
  /** What is not there — "Nothing waiting", not "No data". */
  title: string
  /** Why, or what to do about it. Empty is fine; a wrong guess is not. */
  hint?: ReactNode
  className?: string
}

/**
 * The state a live-query view is in most of the time. It is text, on purpose:
 * "nothing pending" rendered wrong is what makes a working tile look broken,
 * and a spinner that never stops is worse than a sentence.
 */
export function EmptyState({ title, hint, className }: EmptyStateProps): ReactElement {
  return (
    <div className={classes("attn-app-empty", className)}>
      <div className="attn-app-empty-title">{title}</div>
      {hint && <div className="attn-app-empty-hint">{hint}</div>}
    </div>
  )
}

export interface MarkdownProps {
  /** The source. Agent-written content is markdown, which is why this is here. */
  children: string
  className?: string
}

/**
 * Read-only markdown, with GitHub tables and task lists.
 *
 * No raw HTML and no scripts: what a view renders here is usually written by an
 * agent, and an app that wants richer output composes components instead.
 */
export function Markdown({ children, className }: MarkdownProps): ReactElement {
  return (
    <div className={classes("attn-app-markdown", className)}>
      <ReactMarkdown remarkPlugins={[remarkGfm]}>{children}</ReactMarkdown>
    </div>
  )
}
