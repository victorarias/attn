import { useRef, useState } from 'react';
import FocusTrap from 'focus-trap-react';
import { useEscapeStack } from '../../hooks/useEscapeStack';
import './AppViewParamsPrompt.css';

// What a view asks for when it is docked.
//
// The string is opaque to attn: the app declared the label and the placeholder,
// and what the text means is the app's business. It is asked for here — once, at
// dock time — because that is the only moment the answer belongs to the user
// rather than to the app, and because it is what makes two tiles of one view
// show different things.
interface AppViewParamsPromptProps {
  /** "reviewer/approvals" — which view is asking. */
  viewTitle: string;
  /** The app's own wording for the field. */
  label: string;
  placeholder?: string;
  onSubmit: (params: string) => void;
  onClose: () => void;
}

export function AppViewParamsPrompt({
  viewTitle,
  label,
  placeholder,
  onSubmit,
  onClose,
}: AppViewParamsPromptProps) {
  const [value, setValue] = useState('');
  const inputRef = useRef<HTMLInputElement>(null);

  useEscapeStack(onClose, true);

  const submit = () => {
    // An empty answer is a legitimate one — the view is told it got none, and
    // an app that needs it says so far better than a modal that will not close.
    onSubmit(value.trim());
    onClose();
  };

  return (
    <div className="app-view-params-prompt" role="presentation" onClick={onClose}>
      {/* The trap is what makes the field typable. This prompt opens from the
          command menu, whose own trap returns focus to the terminal as it
          closes — after this one has mounted — so a plain focus-on-mount is
          undone and every keystroke goes to the shell instead. */}
      <FocusTrap
        focusTrapOptions={{
          allowOutsideClick: true,
          escapeDeactivates: false,
          initialFocus: () => inputRef.current ?? false,
        }}
      >
        <div
          className="app-view-params-content"
          role="dialog"
          aria-modal="true"
          aria-labelledby="app-view-params-title"
          onClick={(event) => event.stopPropagation()}
        >
          <div className="app-view-params-title" id="app-view-params-title">{viewTitle}</div>
          <label className="app-view-params-label" htmlFor="app-view-params-input">{label}</label>
          <input
            ref={inputRef}
            id="app-view-params-input"
            className="app-view-params-input"
            data-testid="app-view-params-input"
            type="text"
            spellCheck={false}
            placeholder={placeholder}
            value={value}
            onChange={(event) => setValue(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                event.preventDefault();
                submit();
              }
            }}
          />
          <div className="app-view-params-actions">
            <button type="button" className="cancel" onClick={onClose}>Cancel</button>
            <button type="button" className="confirm" data-testid="app-view-params-dock" onClick={submit}>Dock</button>
          </div>
        </div>
      </FocusTrap>
    </div>
  );
}
