import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { AutoModeSettings } from './AutoModeSettings';
import type {
  AutoModeConfigInfo,
  AutoModeProposalInfo,
  AutoModePromotion,
  AutoModeState,
} from '../hooks/daemonAutoModeEvents';
import { useAutoModePolicy } from '../hooks/useAutoModePolicy';

const config = (over: Partial<AutoModeConfigInfo> = {}): AutoModeConfigInfo => ({
  enabled_default: true,
  environment: [],
  allow: [],
  hard_deny: ['*attn automode env*'],
  classifier_models: ['opencode-go/glm-5.3'],
  escalation_models: ['opencode-go/qwen3.8-max'],
  ...over,
});

const proposal = (over: Partial<AutoModeProposalInfo> = {}): AutoModeProposalInfo => ({
  id: 7,
  kind: 'allow',
  target: '',
  value: 'git push origin*',
  proposed_by: 'session-a',
  state: 'pending',
  created_at: '2026-08-16T10:00:00Z',
  resolved_at: '',
  ...over,
});

const state = (over: Partial<AutoModeState> = {}): AutoModeState => ({
  config: config(),
  proposals: [],
  denials: [],
  ...over,
});

// The section is a view over the hook; driving both together is what makes the
// promote flow — act, re-read, redraw — an assertion rather than three.
function Harness(props: {
  getState: () => Promise<AutoModeState>;
  promoteProposal: (id: number) => Promise<AutoModePromotion>;
  discardProposal: (id: number) => Promise<AutoModePromotion>;
}) {
  const policy = useAutoModePolicy({ enabled: true, ...props });
  return <AutoModeSettings policy={policy} />;
}

const resolved = (): AutoModePromotion => ({ proposal: proposal(), config: config() });

function renderPane(value: AutoModeState, over: Partial<{
  promoteProposal: (id: number) => Promise<AutoModePromotion>;
  discardProposal: (id: number) => Promise<AutoModePromotion>;
}> = {}) {
  const getState = vi.fn().mockResolvedValue(value);
  const promoteProposal = over.promoteProposal ?? vi.fn().mockResolvedValue(resolved());
  const discardProposal = over.discardProposal ?? vi.fn().mockResolvedValue(resolved());
  render(
    <Harness
      getState={getState}
      promoteProposal={promoteProposal}
      discardProposal={discardProposal}
    />,
  );
  return { getState, promoteProposal, discardProposal };
}

describe('AutoModeSettings', () => {
  it('reads auto mode once on open and does not loop', async () => {
    const { getState } = renderPane(state());
    await waitFor(() => screen.getByTestId('automode-config'));

    expect(getState).toHaveBeenCalledTimes(1);
    await new Promise((done) => setTimeout(done, 50));
    expect(getState).toHaveBeenCalledTimes(1);
  });

  // The list is the whole reason this section exists. An empty one has to read
  // as "nobody proposed anything", never as a panel that failed to load.
  it('explains an empty proposal list rather than showing nothing', async () => {
    renderPane(state());
    const empty = await screen.findByTestId('automode-no-proposals');
    expect(empty).toHaveTextContent('No proposals are waiting');
    expect(screen.queryByTestId('automode-proposals')).toBeNull();
  });

  it('shows what each proposal asks for and who asked', async () => {
    renderPane(state({
      proposals: [
        proposal(),
        proposal({ id: 8, kind: 'model', target: 'classifier', value: 'opencode-go/other', proposed_by: '' }),
      ],
    }));
    await screen.findByTestId('automode-proposals');

    const allow = screen.getByTestId('automode-proposal-7');
    expect(allow).toHaveTextContent('allow');
    expect(allow).toHaveTextContent('git push origin*');
    expect(allow).toHaveTextContent('session-a');

    const model = screen.getByTestId('automode-proposal-8');
    expect(model).toHaveTextContent('classifier model');
    // A CLI caller that named nobody is still shown, rather than left blank.
    expect(model).toHaveTextContent('unattributed');
  });

  // A layer's models are walked in order, and which one judges is the whole
  // difference between a fallback list and a list somebody reads as a pool.
  it('shows each layer\'s models in order, marking the one that judges', async () => {
    renderPane(state({
      config: config({ classifier_models: ['opencode-go/glm-5.3', 'vendor/backup'] }),
    }));
    const classifier = await screen.findByTestId('automode-classifier-models');
    expect(classifier).toHaveTextContent('opencode-go/glm-5.3 judges');
    expect(classifier).toHaveTextContent('vendor/backup fallback');

    const escalation = screen.getByTestId('automode-escalation-models');
    expect(escalation).toHaveTextContent('opencode-go/qwen3.8-max judges');
    expect(escalation).not.toHaveTextContent('fallback');
  });

  // Promotion is auto mode's trust boundary: this button is the only thing in
  // the system that turns a proposal into policy.
  it('promotes a proposal and re-reads the result', async () => {
    const promoteProposal = vi.fn().mockResolvedValue(resolved());
    const { getState } = renderPane(state({ proposals: [proposal()] }), { promoteProposal });
    await screen.findByTestId('automode-proposals');

    fireEvent.click(screen.getByTestId('automode-promote-7'));
    await waitFor(() => expect(promoteProposal).toHaveBeenCalledWith(7));
    // The daemon is authoritative: the config shown is a re-read, not a guess
    // about what promoting did to it.
    await waitFor(() => expect(getState).toHaveBeenCalledTimes(2));
  });

  // The way in needs the way out: a proposal nobody wants has to be closable
  // from the same list, or it sits there forever.
  it('discards a proposal and re-reads the result', async () => {
    const discardProposal = vi.fn().mockResolvedValue(resolved());
    const { getState } = renderPane(state({ proposals: [proposal()] }), { discardProposal });
    await screen.findByTestId('automode-proposals');

    fireEvent.click(screen.getByTestId('automode-discard-7'));
    await waitFor(() => expect(discardProposal).toHaveBeenCalledWith(7));
    await waitFor(() => expect(getState).toHaveBeenCalledTimes(2));
  });

  it('shows the failure when a promotion is refused and keeps the list', async () => {
    const promoteProposal = vi.fn().mockRejectedValue(new Error('auto mode proposal 7 is already promoted'));
    renderPane(state({ proposals: [proposal()] }), { promoteProposal });
    await screen.findByTestId('automode-proposals');

    fireEvent.click(screen.getByTestId('automode-promote-7'));
    await waitFor(() => screen.getByText('auto mode proposal 7 is already promoted'));
    // A failed action must not blank a good snapshot.
    expect(screen.getByTestId('automode-proposal-7')).toBeInTheDocument();
  });

  // The policy is shown beside the list so promoting is a decision with the
  // current state in view, not a guess.
  it('shows the effective policy a session would launch with', async () => {
    renderPane(state({
      config: config({
        enabled_default: false,
        allow: ['git push origin*'],
        hard_deny: ['*attn automode env*', 'rm -rf /*'],
        environment: ['never touch prod'],
      }),
    }));
    const shown = await screen.findByTestId('automode-config');

    expect(shown).toHaveTextContent('Auto mode off');
    expect(shown).toHaveTextContent('opencode-go/glm-5.3');
    expect(shown).toHaveTextContent('opencode-go/qwen3.8-max');
    // The shipped denies are resolved in daemon-side, so they show up here
    // without anyone having promoted them.
    expect(screen.getByTestId('automode-hard-deny')).toHaveTextContent('*attn automode env*');
    expect(screen.getByTestId('automode-allow')).toHaveTextContent('git push origin*');
    expect(screen.getByTestId('automode-environment')).toHaveTextContent('never touch prod');
  });

  it('says so when a list is empty rather than leaving a blank row', async () => {
    renderPane(state({ config: config({ hard_deny: [] }) }));
    await screen.findByTestId('automode-config');

    expect(screen.getByTestId('automode-allow')).toHaveTextContent('Nothing skips the classifier');
    expect(screen.getByTestId('automode-hard-deny')).toHaveTextContent('Nothing is refused');
  });

  it('offers a retry when auto mode cannot be read at all', async () => {
    const getState = vi.fn().mockRejectedValue(new Error('no database'));
    render(
      <Harness
        getState={getState}
        promoteProposal={vi.fn()}
        discardProposal={vi.fn()}
      />,
    );

    await waitFor(() => screen.getByText('no database'));
    getState.mockResolvedValue(state());
    fireEvent.click(screen.getByText('Try again'));
    await waitFor(() => screen.getByTestId('automode-config'));
  });
});
