import { beforeEach, describe, expect, it, vi } from "vitest";
import "./runtime";

const ELEMENT_KEY = "element-6066-11e4-a52e-4f735466cecf";
const SHADOW_KEY = "shadow-6066-11e4-a52e-4f735466cecf";

function runtime() {
  const value = window.__attnBrowser;
  if (!value) throw new Error("browser runtime was not installed");
  return value;
}

describe("browser runtime", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
    void runtime().execute("release_actions", {});
    void runtime().execute("switch_to_frame", { id: null });
  });

  it("finds semantic targets and operates them through stable references", async () => {
    document.body.innerHTML = `
      <label>Search <input /></label>
      <button>Submit</button>
    `;

    const input = await runtime().execute("find_element", { using: "label", value: "Search" }) as Record<string, string>;
    const button = await runtime().execute("find_element", { using: "role", value: "button", name: "Submit" }) as Record<string, string>;

    await runtime().execute("send_keys_to_element", { element: input[ELEMENT_KEY], text: "attn" });
    await runtime().execute("click_element", { element: button[ELEMENT_KEY] });

    expect((document.querySelector("input") as HTMLInputElement).value).toBe("attn");
    expect(input[ELEMENT_KEY]).toMatch(/^attn-element-/);
  });

  it("redacts passwords from snapshots without truncating typed input", async () => {
    document.body.innerHTML = `<input type="password" value="secret" />`;
    const input = document.querySelector("input") as HTMLInputElement;
    input.setSelectionRange(input.value.length, input.value.length);
    const inputRef = await runtime().execute("find_element", {
      using: "css",
      value: "input",
    }) as Record<string, string>;

    const snapshot = await runtime().execute("snapshot", {}) as {
      elements: Array<{ name: string }>;
    };
    await runtime().execute("send_keys_to_element", {
      element: inputRef[ELEMENT_KEY],
      text: "-more",
    });

    expect(snapshot.elements[0]?.name).toBe("");
    expect(input.value).toBe("secret-more");
  });

  it("finds elements inside shadow roots", async () => {
    const host = document.createElement("div");
    host.attachShadow({ mode: "open" }).innerHTML = "<button>Inside</button>";
    document.body.append(host);
    const hostRef = await runtime().execute("find_element", { using: "css", value: "div" }) as Record<string, string>;
    const shadowRef = await runtime().execute("get_element_shadow_root", { element: hostRef[ELEMENT_KEY] }) as Record<string, string>;
    const buttonRef = await runtime().execute("find_element_from_shadow", {
      shadow: shadowRef[SHADOW_KEY], using: "text", value: "Inside",
    }) as Record<string, string>;

    expect(shadowRef[SHADOW_KEY]).toMatch(/^attn-shadow-/);
    expect(buttonRef[ELEMENT_KEY]).toMatch(/^attn-element-/);
  });

  it("waits for dynamic content", async () => {
    window.setTimeout(() => {
      document.body.innerHTML = "<p>Ready</p>";
    }, 10);

    const result = await runtime().execute("wait_for", {
      using: "text", value: "Ready", state: "attached", timeout: 500,
    }) as Record<string, string>;

    expect(result[ELEMENT_KEY]).toMatch(/^attn-element-/);
  });

  it("edits controls in a same-origin frame", async () => {
    const frame = document.createElement("iframe");
    document.body.append(frame);
    const frameDocument = frame.contentDocument;
    if (!frameDocument) throw new Error("frame document unavailable");
    frameDocument.body.innerHTML = "<label>Frame field <input></label><button>Frame button</button>";
    const frameRef = await runtime().execute("find_element", { using: "css", value: "iframe" }) as Record<string, string>;
    await runtime().execute("switch_to_frame", { id: frameRef });
    const inputRef = await runtime().execute("find_element", { using: "label", value: "Frame field" }) as Record<string, string>;
    await runtime().execute("send_keys_to_element", { element: inputRef[ELEMENT_KEY], text: "inside" });
    const input = frameDocument.querySelector("input") as HTMLInputElement;
    input.focus();
    await runtime().execute("perform_actions", {
      actions: [{
        type: "key",
        id: "keyboard",
        actions: [
          { type: "keyDown", value: "x" },
          { type: "keyUp", value: "x" },
        ],
      }],
    });

    const button = frameDocument.querySelector("button") as HTMLButtonElement;
    const clicked = vi.fn();
    button.addEventListener("click", clicked);
    vi.spyOn(frameDocument, "elementFromPoint").mockReturnValue(button);
    await runtime().execute("perform_actions", {
      actions: [{
        type: "pointer",
        id: "mouse",
        actions: [
          { type: "pointerMove", x: 10, y: 10 },
          { type: "pointerDown", button: 0 },
          { type: "pointerUp", button: 0 },
        ],
      }],
    });

    expect(input.value).toBe("insidex");
    expect(clicked).toHaveBeenCalledTimes(1);
    expect(frameDocument.activeElement).toBe(button);
    const scriptValue = await runtime().execute("execute_script", {
      script: "return document.querySelector('input').value;",
      args: [],
    });
    expect(scriptValue).toBe("insidex");
    await runtime().execute("switch_to_parent_frame", {});
  });

  it("keeps history navigation on the top-level page after switching frames", async () => {
    const frame = document.createElement("iframe");
    document.body.append(frame);
    const frameRef = await runtime().execute("find_element", {
      using: "css",
      value: "iframe",
    }) as Record<string, string>;
    await runtime().execute("switch_to_frame", { id: frameRef });
    const topBack = vi.spyOn(window.history, "back").mockImplementation(() => {});
    const topForward = vi.spyOn(window.history, "forward").mockImplementation(() => {});
    const frameBack = vi.spyOn(frame.contentWindow!.history, "back").mockImplementation(() => {});
    const frameForward = vi.spyOn(frame.contentWindow!.history, "forward").mockImplementation(() => {});

    await runtime().execute("back", {});
    await runtime().execute("forward", {});

    expect(topBack).toHaveBeenCalledTimes(1);
    expect(topForward).toHaveBeenCalledTimes(1);
    expect(frameBack).not.toHaveBeenCalled();
    expect(frameForward).not.toHaveBeenCalled();
  });

  it("applies Enter default behavior to the focused form control", async () => {
    document.body.innerHTML = `<form><input name="query" /></form>`;
    const input = document.querySelector("input") as HTMLInputElement;
    const submitted = vi.fn((event: Event) => event.preventDefault());
    document.querySelector("form")?.addEventListener("submit", submitted);
    input.focus();

    await runtime().execute("perform_actions", {
      actions: [{
        type: "key",
        id: "keyboard",
        actions: [
          { type: "keyDown", value: "Enter" },
          { type: "keyUp", value: "Enter" },
        ],
      }],
    });

    expect(submitted).toHaveBeenCalledTimes(1);
  });

  it("keeps pointer coordinates across action ticks", async () => {
    document.body.innerHTML = `<button>Target</button>`;
    const button = document.querySelector("button") as HTMLButtonElement;
    const clicked = vi.fn();
    button.addEventListener("click", clicked);
    vi.spyOn(document, "elementFromPoint").mockImplementation((x, y) => (
      x === 40 && y === 30 ? button : null
    ));

    await runtime().execute("perform_actions", {
      actions: [{
        type: "pointer",
        id: "mouse",
        actions: [
          { type: "pointerMove", x: 40, y: 30 },
          { type: "pointerDown", button: 0 },
          { type: "pointerUp", button: 0 },
        ],
      }],
    });

    expect(clicked).toHaveBeenCalledTimes(1);
    expect(button).toHaveFocus();
  });

  it("focuses clicked controls before subsequent keyboard actions", async () => {
    document.body.innerHTML = `<input value="" />`;
    const input = document.querySelector("input") as HTMLInputElement;
    const inputRef = await runtime().execute("find_element", {
      using: "css",
      value: "input",
    }) as Record<string, string>;

    await runtime().execute("click_element", {
      element: inputRef[ELEMENT_KEY],
    });
    await runtime().execute("perform_actions", {
      actions: [{
        type: "key",
        id: "keyboard",
        actions: [
          { type: "keyDown", value: "x" },
          { type: "keyUp", value: "x" },
        ],
      }],
    });

    expect(input).toHaveFocus();
    expect(input.value).toBe("x");
  });

  it("does not insert printable keys while a command modifier is held", async () => {
    document.body.innerHTML = `<input value="seed" />`;
    const input = document.querySelector("input") as HTMLInputElement;
    input.focus();

    await runtime().execute("perform_actions", {
      actions: [{
        type: "key",
        id: "keyboard",
        actions: [
          { type: "keyDown", value: "Control" },
          { type: "keyDown", value: "a" },
          { type: "keyUp", value: "a" },
          { type: "keyUp", value: "Control" },
        ],
      }],
    });

    expect(input.value).toBe("seed");
  });

  it("starts input sources in the same action tick concurrently", async () => {
    vi.useFakeTimers();
    try {
      document.body.innerHTML = `<input value="" />`;
      const input = document.querySelector("input") as HTMLInputElement;
      input.focus();

      const actions = runtime().execute("perform_actions", {
        actions: [
          {
            type: "none",
            id: "delay",
            actions: [{ type: "pause", duration: 1_000 }],
          },
          {
            type: "key",
            id: "keyboard",
            actions: [{ type: "keyDown", value: "x" }],
          },
        ],
      });

      await vi.advanceTimersByTimeAsync(0);
      expect(input.value).toBe("x");
      await vi.advanceTimersByTimeAsync(1_000);
      await actions;
    } finally {
      vi.useRealTimers();
    }
  });
});
