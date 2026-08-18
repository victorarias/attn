import type { ReactElement, ReactNode } from "react";
/**
 * `primary` is the one action a view wants; `secondary` is everything else;
 * `danger` is the one that destroys something. There is no fourth: a view that
 * needs another meaning is telling the SDK about a component it is missing.
 */
export type ButtonVariant = "primary" | "secondary" | "danger";
export interface ButtonProps {
    children?: ReactNode;
    variant?: ButtonVariant;
    disabled?: boolean;
    onClick?: () => void;
    /** Buttons in a view are actions, never form submissions, so this defaults to "button". */
    type?: "button" | "submit";
    title?: string;
    className?: string;
}
export declare function Button({ children, variant, disabled, onClick, type, title, className, }: ButtonProps): ReactElement;
export interface TextInputProps {
    value: string;
    onChange: (value: string) => void;
    placeholder?: string;
    disabled?: boolean;
    /** Rendered under the field, in the error colour. A refusal the user can read. */
    error?: string;
    ariaLabel?: string;
    className?: string;
}
export declare function TextInput({ value, onChange, placeholder, disabled, error, ariaLabel, className, }: TextInputProps): ReactElement;
export interface TextAreaProps extends TextInputProps {
    /** Visible rows. The box does not grow on its own — a view that wants that says so. */
    rows?: number;
}
export declare function TextArea({ value, onChange, placeholder, disabled, error, ariaLabel, className, rows, }: TextAreaProps): ReactElement;
export interface ListProps {
    children?: ReactNode;
    className?: string;
}
/** The scrolling column a view's rows live in. */
export declare function List({ children, className }: ListProps): ReactElement;
export interface ListRowProps {
    /** The line the user reads first. */
    title: ReactNode;
    /** The quieter second line: who, when, which session. */
    meta?: ReactNode;
    /** Trailing controls, laid out at the row's end. */
    actions?: ReactNode;
    /** A row that answers a click is focusable and reachable by keyboard. */
    onClick?: () => void;
    selected?: boolean;
    className?: string;
}
export declare function ListRow({ title, meta, actions, onClick, selected, className, }: ListRowProps): ReactElement;
export interface EmptyStateProps {
    /** What is not there — "Nothing waiting", not "No data". */
    title: string;
    /** Why, or what to do about it. Empty is fine; a wrong guess is not. */
    hint?: ReactNode;
    className?: string;
}
/**
 * The state a live-query view is in most of the time. It is text, on purpose:
 * "nothing pending" rendered wrong is what makes a working tile look broken,
 * and a spinner that never stops is worse than a sentence.
 */
export declare function EmptyState({ title, hint, className }: EmptyStateProps): ReactElement;
export interface MarkdownProps {
    /** The source. Agent-written content is markdown, which is why this is here. */
    children: string;
    className?: string;
}
/**
 * Read-only markdown, with GitHub tables and task lists.
 *
 * No raw HTML and no scripts: what a view renders here is usually written by an
 * agent, and an app that wants richer output composes components instead.
 */
export declare function Markdown({ children, className }: MarkdownProps): ReactElement;
