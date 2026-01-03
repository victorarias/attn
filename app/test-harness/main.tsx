/**
 * Test Harness Entry Point
 *
 * Loads component harnesses based on ?component= URL parameter.
 * Usage: /test-harness/?component=ReviewPanel
 */
import React from 'react';
import { createRoot } from 'react-dom/client';
import { harnesses } from './harnesses';
import type { HarnessAPI } from './types';
// Note: Base styles are included via component CSS imports

// Initialize the harness API
const initHarnessAPI = (componentName: string): HarnessAPI => ({
  componentName,
  calls: {},
  ready: false,

  recordCall(fnName: string, args: unknown[]) {
    if (!this.calls[fnName]) {
      this.calls[fnName] = [];
    }
    this.calls[fnName].push(args);
  },

  getCalls(fnName: string) {
    return this.calls[fnName] || [];
  },

  clearCalls() {
    this.calls = {};
  },

  triggerRerender: () => {
    console.warn('triggerRerender not yet initialized');
  },
});

function HarnessApp() {
  const params = new URLSearchParams(window.location.search);
  const componentName = params.get('component');

  if (!componentName) {
    return (
      <div className="harness-error">
        <div>
          <h1>No component specified</h1>
          <p>
            Add <code>?component=ComponentName</code> to the URL.
          </p>
          <p style={{ marginTop: 16 }}>
            Available: {Object.keys(harnesses).join(', ') || 'none'}
          </p>
        </div>
      </div>
    );
  }

  const HarnessComponent = harnesses[componentName];

  if (!HarnessComponent) {
    return (
      <div className="harness-error">
        <div>
          <h1>Unknown component: {componentName}</h1>
          <p>
            Available: {Object.keys(harnesses).join(', ') || 'none'}
          </p>
        </div>
      </div>
    );
  }

  // Initialize harness API for this component
  window.__HARNESS__ = initHarnessAPI(componentName);

  const handleReady = () => {
    window.__HARNESS__.ready = true;
    console.log(`[Harness] ${componentName} ready`);
  };

  const setTriggerRerender = (fn: () => void) => {
    window.__HARNESS__.triggerRerender = fn;
  };

  return (
    <HarnessComponent onReady={handleReady} setTriggerRerender={setTriggerRerender} />
  );
}

const root = createRoot(document.getElementById('harness-root')!);
root.render(
  <React.StrictMode>
    <HarnessApp />
  </React.StrictMode>
);
