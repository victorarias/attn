const EDITABLE_SELECTOR = 'input, textarea, [contenteditable]';
const VERBATIM_ENTRY_ATTRIBUTES = {
  autocorrect: 'off',
  autocapitalize: 'none',
  spellcheck: 'false',
} as const;

function isEditableElement(node: Node): node is HTMLElement {
  return node instanceof HTMLElement && node.matches(EDITABLE_SELECTOR);
}

function enforceVerbatimTextEntry(element: HTMLElement): void {
  for (const [attribute, value] of Object.entries(VERBATIM_ENTRY_ATTRIBUTES)) {
    if (element.getAttribute(attribute) !== value) {
      element.setAttribute(attribute, value);
    }
  }
}

function enforceInSubtree(root: ParentNode | Node): void {
  if (isEditableElement(root as Node)) {
    enforceVerbatimTextEntry(root as HTMLElement);
  }

  if ('querySelectorAll' in root) {
    root.querySelectorAll<HTMLElement>(EDITABLE_SELECTOR).forEach(enforceVerbatimTextEntry);
  }
}

/**
 * Text entry in attn is preserved as typed, including controls created later
 * by xterm and editor widgets.
 */
export function installVerbatimTextEntryGuard(root: Document | HTMLElement): () => void {
  enforceInSubtree(root);

  const observedRoot = root instanceof Document ? root.documentElement : root;
  const observer = new MutationObserver((mutations) => {
    for (const mutation of mutations) {
      if (mutation.type === 'attributes') {
        enforceVerbatimTextEntry(mutation.target as HTMLElement);
        continue;
      }

      mutation.addedNodes.forEach(enforceInSubtree);
    }
  });

  observer.observe(observedRoot, {
    subtree: true,
    childList: true,
    attributes: true,
    attributeFilter: Object.keys(VERBATIM_ENTRY_ATTRIBUTES),
  });

  return () => observer.disconnect();
}
