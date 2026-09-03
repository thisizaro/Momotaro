// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { AgentConfigPanel } from '@/components/AgentConfigPanel';
import type { DemoConfigResponse } from '@/types';

afterEach(cleanup);

function makeConfig(overrides: Partial<DemoConfigResponse> = {}): DemoConfigResponse {
  return {
    demo_time_scale: 300000,
    max_retries: 3,
    max_contacts: 3,
    contact_cooldown_seconds: 86400,
    recovery_window_seconds: 604800,
    llm_sample_rate: 0.15,
    route_confidence_threshold: 0.8,
    classify_confidence_threshold: 0.4,
    nudge_max_chars: 160,
    downtime_max_unresolved_hold_seconds: 21600,
    ...overrides,
  };
}

/**
 * The read-only config panel (docs/DEMO_READINESS.md Unit AM): what the
 * agent is bounded by, fixed at process startup, never adjustable from
 * this UI. These tests pin the three things the brief calls out
 * explicitly: it renders, it says plainly this is not the full env var
 * list, and it never crashes on a missing value.
 */
describe('AgentConfigPanel', () => {
  it('shows a loading skeleton while config has not arrived yet', () => {
    const { container } = render(<AgentConfigPanel config={null} error={null} onRetry={() => {}} />);
    expect(container.querySelector('.animate-pulse')).toBeInTheDocument();
  });

  it('shows an error with a retry action when loading failed', () => {
    const onRetry = vi.fn();
    render(<AgentConfigPanel config={null} error="Failed to load agent config" onRetry={onRetry} />);
    expect(screen.getByText('Failed to load agent config')).toBeInTheDocument();
    screen.getByText('Retry').click();
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it('states plainly that this is fixed at startup and not the full variable list', () => {
    render(<AgentConfigPanel config={makeConfig()} error={null} onRetry={() => {}} />);
    expect(screen.getByText(/fixed at (process )?startup/i)).toBeInTheDocument();
    // The honest-count note (docs/DEMO_READINESS.md brief: state the real
    // count once). 60 is the count this unit verified against
    // .env.example with `grep -cE '^[A-Z_][A-Z0-9_]*=' .env.example`.
    expect(screen.getByText(/60 environment variables/i)).toBeInTheDocument();
  });

  it('renders the guardrail values grouped under retry and contact limits', () => {
    render(<AgentConfigPanel config={makeConfig()} error={null} onRetry={() => {}} />);
    expect(screen.getByText(/3 attempts/i)).toBeInTheDocument();
    expect(screen.getByText(/3 contacts/i)).toBeInTheDocument();
  });

  it('renders the time compression group with the configured scale factor', () => {
    render(<AgentConfigPanel config={makeConfig()} error={null} onRetry={() => {}} />);
    expect(screen.getByText(/300,000/)).toBeInTheDocument();
  });

  it('renders the LLM routing group with the sample rate as a percentage', () => {
    render(<AgentConfigPanel config={makeConfig()} error={null} onRetry={() => {}} />);
    expect(screen.getByText(/15%/)).toBeInTheDocument();
  });

  it('never crashes when every numeric value is zero', () => {
    const zeroed = makeConfig({
      demo_time_scale: 0,
      max_retries: 0,
      max_contacts: 0,
      contact_cooldown_seconds: 0,
      recovery_window_seconds: 0,
      llm_sample_rate: 0,
      route_confidence_threshold: 0,
      classify_confidence_threshold: 0,
      nudge_max_chars: 0,
      downtime_max_unresolved_hold_seconds: 0,
    });
    expect(() => render(<AgentConfigPanel config={zeroed} error={null} onRetry={() => {}} />)).not.toThrow();
  });
});
