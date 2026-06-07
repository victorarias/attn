// Adapted from the MIT-licensed tauri-plugin-webdriver executor. attn keeps the
// WebDriver-shaped operations but runs them inside a dynamic child Webview.
import {
  queryAllByAltText,
  queryAllByLabelText,
  queryAllByPlaceholderText,
  queryAllByRole,
  queryAllByTestId,
  queryAllByText,
  queryAllByTitle,
} from "@testing-library/dom";

const ELEMENT_KEY = "element-6066-11e4-a52e-4f735466cecf";
const SHADOW_KEY = "shadow-6066-11e4-a52e-4f735466cecf";

type JsonObject = Record<string, unknown>;
type SearchRoot = Document | Element | ShadowRoot;

interface BrowserRuntime {
  version: number;
  execute(action: string, params: JsonObject): unknown | Promise<unknown>;
}

declare global {
  interface Window {
    __attnBrowser?: BrowserRuntime;
  }
}

const elements = new Map<string, Element>();
const shadows = new Map<string, ShadowRoot>();
let nextElementId = 1;
let nextShadowId = 1;
let frameWindow: Window = window;

function error(message: string): never {
  throw new Error(message);
}

function object(value: unknown): JsonObject {
  return value && typeof value === "object" && !Array.isArray(value) ? value as JsonObject : {};
}

function stringParam(params: JsonObject, key: string, required = true): string {
  const value = params[key];
  if (typeof value === "string" && (!required || value.length > 0)) return value;
  if (!required) return "";
  return error(`${key} must be a non-empty string`);
}

function numberParam(params: JsonObject, key: string, fallback: number): number {
  return typeof params[key] === "number" && Number.isFinite(params[key]) ? params[key] as number : fallback;
}

function currentDocument(): Document {
  try {
    return frameWindow.document;
  } catch {
    return error("the selected frame is cross-origin and cannot be automated by the child Webview");
  }
}

function isDocument(value: unknown): value is Document {
  return Boolean(value) && (value as Node).nodeType === 9;
}

function isElement(value: unknown): value is Element {
  return Boolean(value) && (value as Node).nodeType === 1;
}

function elementReference(element: Element): JsonObject {
  for (const [id, existing] of elements) {
    if (existing === element) return { [ELEMENT_KEY]: id };
  }
  const id = `attn-element-${nextElementId++}`;
  elements.set(id, element);
  return { [ELEMENT_KEY]: id };
}

function shadowReference(root: ShadowRoot): JsonObject {
  for (const [id, existing] of shadows) {
    if (existing === root) return { [SHADOW_KEY]: id };
  }
  const id = `attn-shadow-${nextShadowId++}`;
  shadows.set(id, root);
  return { [SHADOW_KEY]: id };
}

function requireElement(params: JsonObject, key = "element"): Element {
  const raw = params[key];
  const id = typeof raw === "string" ? raw : object(raw)[ELEMENT_KEY];
  if (typeof id !== "string") return error(`${key} must be an element reference`);
  const element = elements.get(id);
  if (!element || !element.isConnected) return error(`stale element reference: ${id}`);
  return element;
}

function requireShadow(params: JsonObject): ShadowRoot {
  const raw = params.shadow;
  const id = typeof raw === "string" ? raw : object(raw)[SHADOW_KEY];
  if (typeof id !== "string") return error("shadow must be a shadow root reference");
  const root = shadows.get(id);
  if (!root || !root.host.isConnected) return error(`detached shadow root: ${id}`);
  return root;
}

function rootFor(action: string, params: JsonObject): SearchRoot {
  if (action.includes("_from_element")) return requireElement(params);
  if (action.includes("_from_shadow")) return requireShadow(params);
  return currentDocument();
}

function locatorMatches(root: SearchRoot, params: JsonObject): Element[] {
  const using = stringParam(params, "using").toLowerCase();
  const value = stringParam(params, "value");
  const exact = params.exact !== false;
  const htmlRoot = root as HTMLElement;
  switch (using) {
    case "css selector":
    case "css":
      return Array.from(root.querySelectorAll(value));
    case "xpath": {
      const doc = isDocument(root) ? root : root.ownerDocument;
      const result = doc.evaluate(value, root, null, XPathResult.ORDERED_NODE_SNAPSHOT_TYPE, null);
      const matches: Element[] = [];
      for (let index = 0; index < result.snapshotLength; index++) {
        const node = result.snapshotItem(index);
        if (isElement(node)) matches.push(node);
      }
      return matches;
    }
    case "tag name":
      return Array.from(root.querySelectorAll(value));
    case "link text":
      return Array.from(root.querySelectorAll("a")).filter((node) => node.textContent?.trim() === value);
    case "partial link text":
      return Array.from(root.querySelectorAll("a")).filter((node) => node.textContent?.includes(value));
    case "role":
      return queryAllByRole(htmlRoot, value, {
        name: typeof params.name === "string" ? params.name : undefined,
        hidden: params.hidden === true,
      });
    case "text":
      return queryAllByText(htmlRoot, value, { exact });
    case "label":
      return queryAllByLabelText(htmlRoot, value, { exact });
    case "placeholder":
      return queryAllByPlaceholderText(htmlRoot, value, { exact });
    case "alt":
      return queryAllByAltText(htmlRoot, value, { exact });
    case "title":
      return queryAllByTitle(htmlRoot, value, { exact });
    case "testid":
    case "test id":
      return queryAllByTestId(htmlRoot, value, { exact });
    default:
      return error(`unsupported locator strategy: ${using}`);
  }
}

function find(action: string, params: JsonObject, multiple: boolean): unknown {
  const matches = locatorMatches(rootFor(action, params), params);
  if (multiple) return matches.map(elementReference);
  if (matches.length === 0) return error(`no such element: ${params.using}=${params.value}`);
  return elementReference(matches[0]);
}

function visible(element: Element): boolean {
  const style = (element.ownerDocument.defaultView || window).getComputedStyle(element);
  const htmlElement = element as HTMLElement;
  return style.display !== "none" && style.visibility !== "hidden" && style.opacity !== "0"
    && (!("offsetParent" in htmlElement) || htmlElement.offsetParent !== null || style.position === "fixed");
}

function hasTag(element: Element, tag: string): boolean {
  return element.tagName.toLowerCase() === tag;
}

function editableValue(element: Element): string {
  const control = element as HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement;
  if (hasTag(element, "input") || hasTag(element, "textarea") || hasTag(element, "select")) {
    return control.value;
  }
  return element.textContent || "";
}

function snapshotValue(element: Element): string {
  if (hasTag(element, "input") && (element as HTMLInputElement).type === "password") return "";
  return editableValue(element);
}

function isEditable(element: Element): boolean {
  return hasTag(element, "input")
    || hasTag(element, "textarea")
    || hasTag(element, "select")
    || (element as HTMLElement).isContentEditable;
}

function setEditableValue(element: Element, value: string): void {
  element.scrollIntoView({ block: "center", inline: "center" });
  (element as HTMLElement).focus?.();
  const view = element.ownerDocument.defaultView || window;
  if (hasTag(element, "input") || hasTag(element, "textarea")) {
    const prototype = hasTag(element, "textarea") ? view.HTMLTextAreaElement.prototype : view.HTMLInputElement.prototype;
    Object.getOwnPropertyDescriptor(prototype, "value")?.set?.call(element, value);
  } else if (hasTag(element, "select")) {
    Object.getOwnPropertyDescriptor(view.HTMLSelectElement.prototype, "value")?.set?.call(element, value);
  } else if ((element as HTMLElement).isContentEditable) {
    (element as HTMLElement).textContent = value;
  } else {
    error("element is not editable");
  }
  element.dispatchEvent(new InputEvent("input", { bubbles: true, composed: true, inputType: "insertText", data: value }));
  element.dispatchEvent(new Event("change", { bubbles: true, composed: true }));
}

function clickElement(element: Element): void {
  element.scrollIntoView({ block: "center", inline: "center" });
  (element as HTMLElement).focus?.();
  (element as HTMLElement).click();
}

function serializeScriptValue(value: unknown): unknown {
  if (value && typeof value === "object" && (value as Node).nodeType === Node.ELEMENT_NODE) return elementReference(value as Element);
  if (value && typeof value === "object" && (value as ShadowRoot).host?.nodeType === Node.ELEMENT_NODE) return shadowReference(value as ShadowRoot);
  if (Array.isArray(value)) return value.map(serializeScriptValue);
  if (value && typeof value === "object") {
    return Object.fromEntries(Object.entries(value as JsonObject).map(([key, entry]) => [key, serializeScriptValue(entry)]));
  }
  return value;
}

function deserializeScriptValue(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(deserializeScriptValue);
  if (value && typeof value === "object") {
    const record = value as JsonObject;
    if (typeof record[ELEMENT_KEY] === "string") return requireElement({ element: record });
    if (typeof record[SHADOW_KEY] === "string") return requireShadow({ shadow: record });
    return Object.fromEntries(Object.entries(record).map(([key, entry]) => [key, deserializeScriptValue(entry)]));
  }
  return value;
}

function selectedFrameFunction(script: string): Function {
  const constructor = (frameWindow as unknown as { Function: FunctionConstructor }).Function;
  return new constructor("arguments", script);
}

function executeScript(params: JsonObject): unknown {
  const script = stringParam(params, "script", false);
  const args = Array.isArray(params.args) ? params.args.map(deserializeScriptValue) : [];
  const fn = selectedFrameFunction(script);
  return serializeScriptValue(fn.call(frameWindow, args));
}

async function executeAsyncScript(params: JsonObject): Promise<unknown> {
  const script = stringParam(params, "script", false);
  const args = Array.isArray(params.args) ? params.args.map(deserializeScriptValue) : [];
  const timeout = numberParam(params, "timeout", 10_000);
  return await new Promise((resolve, reject) => {
    const timer = window.setTimeout(() => reject(new Error("async script timed out")), timeout);
    const done = (value: unknown) => {
      window.clearTimeout(timer);
      resolve(serializeScriptValue(value));
    };
    try {
      const fn = selectedFrameFunction(script);
      fn.call(frameWindow, [...args, done]);
    } catch (caught) {
      window.clearTimeout(timer);
      reject(caught);
    }
  });
}

async function waitFor(params: JsonObject): Promise<unknown> {
  const timeout = numberParam(params, "timeout", 5_000);
  const state = typeof params.state === "string" ? params.state : "attached";
  const deadline = Date.now() + timeout;
  for (;;) {
    const matches = locatorMatches(currentDocument(), params);
    const candidate = matches[0];
    const satisfied = state === "detached" ? !candidate
      : state === "hidden" ? !candidate || !visible(candidate)
      : state === "visible" ? !!candidate && visible(candidate)
      : !!candidate;
    if (satisfied) return candidate ? elementReference(candidate) : null;
    if (Date.now() >= deadline) return error(`wait_for timed out waiting for ${state}`);
    await new Promise((resolve) => window.setTimeout(resolve, 50));
  }
}

function snapshot(): JsonObject {
  const doc = currentDocument();
  const interactive = Array.from(doc.querySelectorAll("a,button,input,textarea,select,[role],[contenteditable='true']"))
    .filter(visible)
    .slice(0, 250)
    .map((element) => ({
      ...elementReference(element),
      tag: element.tagName.toLowerCase(),
      role: element.getAttribute("role") || undefined,
      type: element.getAttribute("type") || undefined,
      name: (element.getAttribute("aria-label") || element.textContent || element.getAttribute("placeholder")
        || element.getAttribute("title") || element.getAttribute("alt") || snapshotValue(element))
        .trim().replace(/\s+/g, " ").slice(0, 180),
      disabled: (element as HTMLInputElement).disabled || undefined,
    }));
  return {
    url: frameWindow.location.href,
    title: doc.title,
    text: (doc.body?.innerText || "").trim().replace(/\n{3,}/g, "\n\n").slice(0, 20_000),
    elements: interactive,
  };
}

const webdriverKeys: Record<string, string> = {
  "\uE003": "Backspace",
  "\uE004": "Tab",
  "\uE006": "Enter",
  "\uE007": "Enter",
  "\uE008": "Shift",
  "\uE009": "Control",
  "\uE00A": "Alt",
  "\uE00C": "Escape",
  "\uE00D": " ",
  "\uE012": "ArrowLeft",
  "\uE013": "ArrowUp",
  "\uE014": "ArrowRight",
  "\uE015": "ArrowDown",
  "\uE017": "Delete",
};

interface InputSourceState {
  x: number;
  y: number;
  pressedKeys: Set<string>;
  pointerDownTarget: Element | null;
}

const inputSourceStates = new Map<string, InputSourceState>();

function inputSourceState(id: string): InputSourceState {
  const existing = inputSourceStates.get(id);
  if (existing) return existing;
  const created = { x: 0, y: 0, pressedKeys: new Set<string>(), pointerDownTarget: null };
  inputSourceStates.set(id, created);
  return created;
}

function normalizedKey(value: string): string {
  return webdriverKeys[value] || value;
}

function editableSelection(element: Element): { start: number; end: number } {
  if (hasTag(element, "input") || hasTag(element, "textarea")) {
    const control = element as HTMLInputElement | HTMLTextAreaElement;
    const length = control.value.length;
    return {
      start: control.selectionStart ?? length,
      end: control.selectionEnd ?? length,
    };
  }
  const length = editableValue(element).length;
  return { start: length, end: length };
}

function replaceEditableSelection(element: Element, replacement: string, backwards = false): void {
  const value = editableValue(element);
  let { start, end } = editableSelection(element);
  if (start === end && backwards && start > 0) start -= 1;
  if (start === end && !backwards && end < value.length) end += 1;
  setEditableValue(element, `${value.slice(0, start)}${replacement}${value.slice(end)}`);
  if (hasTag(element, "input") || hasTag(element, "textarea")) {
    const caret = start + replacement.length;
    (element as HTMLInputElement | HTMLTextAreaElement).setSelectionRange(caret, caret);
  }
}

function focusByTab(reverse: boolean): void {
  const doc = currentDocument();
  const focusable = Array.from(doc.querySelectorAll<HTMLElement>(
    "a[href],button,input,select,textarea,[tabindex]:not([tabindex='-1']),[contenteditable='true']",
  )).filter((element) => visible(element) && !element.hasAttribute("disabled"));
  if (focusable.length === 0) return;
  const activeIndex = focusable.indexOf(doc.activeElement as HTMLElement);
  const nextIndex = reverse
    ? (activeIndex <= 0 ? focusable.length - 1 : activeIndex - 1)
    : (activeIndex < 0 || activeIndex === focusable.length - 1 ? 0 : activeIndex + 1);
  focusable[nextIndex].focus();
}

function applyKeyDefault(target: Element, key: string, shiftKey: boolean): void {
  if (key === "Tab") {
    focusByTab(shiftKey);
    return;
  }
  if (key === "Enter") {
    if (hasTag(target, "textarea") || (target as HTMLElement).isContentEditable) {
      replaceEditableSelection(target, "\n");
      return;
    }
    if (hasTag(target, "button") || hasTag(target, "a")) {
      (target as HTMLElement).click();
      return;
    }
    const form = (target as HTMLInputElement).form;
    if (form) form.requestSubmit();
    return;
  }
  if (key === " " && (hasTag(target, "button") || hasTag(target, "a"))) {
    (target as HTMLElement).click();
    return;
  }
  if (key === "Backspace") {
    if (isEditable(target)) replaceEditableSelection(target, "", true);
    return;
  }
  if (key === "Delete") {
    if (isEditable(target)) replaceEditableSelection(target, "");
    return;
  }
  if (key.length === 1 && isEditable(target)) {
    replaceEditableSelection(target, key);
  }
}

function dispatchKey(type: "keydown" | "keyup", value: string, state: InputSourceState): void {
  const key = normalizedKey(value);
  if (type === "keydown") state.pressedKeys.add(key);
  const doc = currentDocument();
  const target = doc.activeElement || doc.body;
  if (!isElement(target)) return;
  const view = doc.defaultView || window;
  const event = new view.KeyboardEvent(type, {
    key,
    code: key,
    bubbles: true,
    cancelable: true,
    composed: true,
    shiftKey: state.pressedKeys.has("Shift"),
    ctrlKey: state.pressedKeys.has("Control"),
    altKey: state.pressedKeys.has("Alt"),
    metaKey: state.pressedKeys.has("Meta"),
  });
  const allowed = target.dispatchEvent(event);
  const commandModifierPressed = state.pressedKeys.has("Control")
    || state.pressedKeys.has("Alt")
    || state.pressedKeys.has("Meta");
  if (
    type === "keydown"
    && allowed
    && !commandModifierPressed
    && !["Shift", "Control", "Alt", "Meta"].includes(key)
  ) {
    applyKeyDefault(target, key, state.pressedKeys.has("Shift"));
  }
  if (type === "keyup") state.pressedKeys.delete(key);
}

async function performInputAction(action: JsonObject, state: InputSourceState): Promise<void> {
  const type = action.type;
  if (type === "pause") {
    await new Promise((resolve) => window.setTimeout(resolve, numberParam(action, "duration", 0)));
  }
  if (type === "keyDown" || type === "keyUp") {
    dispatchKey(type === "keyDown" ? "keydown" : "keyup", stringParam(action, "value"), state);
  }
  if (type === "pointerMove") {
    const doc = currentDocument();
    const view = doc.defaultView || window;
    const origin = action.origin && typeof action.origin === "object"
      ? requireElement({ element: action.origin })
      : null;
    const rect = origin?.getBoundingClientRect();
    const baseX = action.origin === "pointer" ? state.x : rect ? rect.left + rect.width / 2 : 0;
    const baseY = action.origin === "pointer" ? state.y : rect ? rect.top + rect.height / 2 : 0;
    state.x = baseX + numberParam(action, "x", 0);
    state.y = baseY + numberParam(action, "y", 0);
    doc.elementFromPoint(state.x, state.y)?.dispatchEvent(new view.PointerEvent("pointermove", {
      clientX: state.x,
      clientY: state.y,
      bubbles: true,
    }));
  }
  if (type === "pointerDown" || type === "pointerUp") {
    const doc = currentDocument();
    const view = doc.defaultView || window;
    const target = doc.elementFromPoint(state.x, state.y) || doc.activeElement;
    const allowed = target?.dispatchEvent(new view.PointerEvent(type === "pointerDown" ? "pointerdown" : "pointerup", {
      clientX: state.x,
      clientY: state.y,
      bubbles: true,
      cancelable: true,
      button: numberParam(action, "button", 0),
    }));
    if (type === "pointerDown") {
      if (allowed && isElement(target)) (target as HTMLElement).focus?.();
      state.pointerDownTarget = isElement(target) ? target : null;
    } else {
      if (target === state.pointerDownTarget && isElement(target)) (target as HTMLElement).click?.();
      state.pointerDownTarget = null;
    }
  }
  if (type === "scroll") {
    frameWindow.scrollBy({
      left: numberParam(action, "deltaX", 0),
      top: numberParam(action, "deltaY", 0),
      behavior: "instant",
    });
  }
}

async function performActions(params: JsonObject): Promise<null> {
  const sources = Array.isArray(params.actions) ? params.actions.map(object) : [];
  const length = Math.max(0, ...sources.map((source) => Array.isArray(source.actions) ? source.actions.length : 0));
  for (let tick = 0; tick < length; tick++) {
    await Promise.all(sources.map((source) => {
      const action = Array.isArray(source.actions) ? object(source.actions[tick]) : {};
      const state = inputSourceState(typeof source.id === "string" ? source.id : "default");
      return performInputAction(action, state);
    }));
  }
  return null;
}

function execute(action: string, params: JsonObject): unknown | Promise<unknown> {
  switch (action) {
    case "snapshot": return snapshot();
    case "click": {
      const selector = stringParam(params, "selector");
      const element = currentDocument().querySelector(selector);
      if (!element) return error(`no such element: css=${selector}`);
      clickElement(element);
      return { success: true, element: elementReference(element) };
    }
    case "type": {
      const selector = stringParam(params, "selector");
      const element = currentDocument().querySelector(selector);
      if (!element) return error(`no such element: css=${selector}`);
      setEditableValue(element, stringParam(params, "text", false));
      return { success: true, element: elementReference(element) };
    }
    case "get_url": return frameWindow.location.href;
    case "get_title": return currentDocument().title;
    case "get_source": return currentDocument().documentElement.outerHTML;
    case "back": window.history.back(); return null;
    case "forward": window.history.forward(); return null;
    case "find_element": case "find_element_from_element": case "find_element_from_shadow": return find(action, params, false);
    case "find_elements": case "find_elements_from_element": case "find_elements_from_shadow": return find(action, params, true);
    case "wait_for": return waitFor(params);
    case "get_active_element": {
      const active = currentDocument().activeElement;
      return active ? elementReference(active) : error("no active element");
    }
    case "get_element_text": return requireElement(params).textContent || "";
    case "get_element_tag_name": return requireElement(params).tagName.toLowerCase();
    case "get_element_attribute": return requireElement(params).getAttribute(stringParam(params, "name"));
    case "get_element_property": return serializeScriptValue((requireElement(params) as unknown as JsonObject)[stringParam(params, "name")]);
    case "get_element_css_value": {
      const element = requireElement(params);
      return (element.ownerDocument.defaultView || window)
        .getComputedStyle(element)
        .getPropertyValue(stringParam(params, "name"));
    }
    case "get_element_rect": {
      const rect = requireElement(params).getBoundingClientRect();
      return { x: rect.x, y: rect.y, width: rect.width, height: rect.height };
    }
    case "get_element_screenshot_rect": {
      const element = requireElement(params);
      element.scrollIntoView({ block: "center", inline: "center" });
      const rect = element.getBoundingClientRect();
      let left = Math.max(0, rect.left);
      let top = Math.max(0, rect.top);
      let right = Math.min(frameWindow.innerWidth, rect.right);
      let bottom = Math.min(frameWindow.innerHeight, rect.bottom);
      let current = frameWindow;
      while (current !== window) {
        const frame = current.frameElement;
        if (!isElement(frame)) return error("selected frame is detached");
        const frameRect = frame.getBoundingClientRect();
        left += frameRect.left;
        right += frameRect.left;
        top += frameRect.top;
        bottom += frameRect.top;
        left = Math.max(left, frameRect.left);
        top = Math.max(top, frameRect.top);
        right = Math.min(right, frameRect.right);
        bottom = Math.min(bottom, frameRect.bottom);
        current = current.parent;
      }
      left = Math.max(0, left);
      top = Math.max(0, top);
      right = Math.min(window.innerWidth, right);
      bottom = Math.min(window.innerHeight, bottom);
      if (right <= left || bottom <= top) return error("element is outside the browser viewport");
      return { x: left, y: top, width: right - left, height: bottom - top };
    }
    case "is_element_displayed": return visible(requireElement(params));
    case "is_element_enabled": return !(requireElement(params) as HTMLInputElement).disabled;
    case "is_element_selected": {
      const element = requireElement(params) as HTMLInputElement | HTMLOptionElement;
      return hasTag(element, "option") ? (element as HTMLOptionElement).selected
        : hasTag(element, "input") ? (element as HTMLInputElement).checked : false;
    }
    case "get_element_computed_role": return requireElement(params).getAttribute("role") || requireElement(params).tagName.toLowerCase();
    case "get_element_computed_label": {
      const element = requireElement(params);
      return element.getAttribute("aria-label") || element.getAttribute("title") || element.textContent?.trim() || "";
    }
    case "get_element_shadow_root": {
      const root = requireElement(params).shadowRoot;
      return root ? shadowReference(root) : error("element has no shadow root");
    }
    case "click_element": {
      const element = requireElement(params);
      clickElement(element);
      return null;
    }
    case "clear_element": setEditableValue(requireElement(params), ""); return null;
    case "send_keys_to_element": setEditableValue(requireElement(params), editableValue(requireElement(params)) + stringParam(params, "text", false)); return null;
    case "select_option": {
      const select = requireElement(params);
      if (!hasTag(select, "select")) return error("element is not a select");
      const selectElement = select as HTMLSelectElement;
      const values = Array.isArray(params.values) ? params.values.map(String) : [stringParam(params, "value")];
      Array.from(selectElement.options).forEach((option) => { option.selected = values.includes(option.value) || values.includes(option.label); });
      selectElement.dispatchEvent(new Event("change", { bubbles: true }));
      return Array.from(selectElement.selectedOptions).map((option) => option.value);
    }
    case "check": {
      const element = requireElement(params);
      const input = element as HTMLInputElement;
      if (!hasTag(element, "input") || !["checkbox", "radio"].includes(input.type)) return error("element is not checkable");
      if (!input.checked) input.click();
      return input.checked;
    }
    case "execute_script": return executeScript(params);
    case "execute_async_script": return executeAsyncScript(params);
    case "perform_actions": return performActions(params);
    case "release_actions": inputSourceStates.clear(); return null;
    case "switch_to_frame": {
      if (params.id === null || params.id === undefined) { frameWindow = window; return null; }
      const frame = typeof params.id === "number"
        ? currentDocument().querySelectorAll("iframe,frame")[params.id]
        : requireElement({ element: params.id });
      const frameElement = frame as HTMLIFrameElement;
      if (!hasTag(frameElement, "iframe") && !hasTag(frameElement, "frame")) return error("no such frame");
      if (!frameElement.contentWindow) return error("no such frame");
      frameWindow = frameElement.contentWindow;
      currentDocument();
      return null;
    }
    case "switch_to_parent_frame": frameWindow = frameWindow.parent || window; currentDocument(); return null;
    default: return error(`unsupported page action: ${action}`);
  }
}

window.__attnBrowser = { version: 1, execute };
