// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, render, screen } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { BaselineComparisonCard } from '@/components/BaselineComparison';
import type { BaselineComparison } from '@/types';

afterEach(cleanup);

const baseline: BaselineComparison = {
  policy_name: 'naive_retry3_nudge1',
  gross_recovered_paise: 3200000,
  intervention_spend_paise: 410000,
  net_recovered_paise: 2790000,
  note: 'Evaluated analytically against the same sealed ground truth using a fixed naive policy.',
};

describe('BaselineComparisonCard', () => {
  it('renders the comparison when a baseline is present', () => {
    render(<BaselineComparisonCard baseline={baseline} ownNetRecoveredPaise={3325500} source="demo:normal" />);
    expect(screen.getByText(/naive_retry3_nudge1/)).toBeInTheDocument();
  });

  it('explains a webhook-sourced batch has no baseline because it has no sealed answer key', () => {
    render(<BaselineComparisonCard baseline={undefined} ownNetRecoveredPaise={0} source="webhook" />);
    const text = screen.getByText(/no ground truth/i).textContent ?? '';
    expect(text.toLowerCase()).toContain('webhook');
    expect(text.toLowerCase()).toContain('demo controls');
  });

  it('still explains an absent baseline honestly when the source is unknown', () => {
    render(<BaselineComparisonCard baseline={undefined} ownNetRecoveredPaise={0} />);
    expect(screen.getByText(/no ground truth/i)).toBeInTheDocument();
  });

  it('never renders an em dash', () => {
    const emDash = String.fromCharCode(0x2014); // banned repo-wide; written this way so no literal em dash sits in this file's own source
    const { container } = render(<BaselineComparisonCard baseline={undefined} ownNetRecoveredPaise={0} source="webhook" />);
    expect(container.textContent).not.toContain(emDash);
  });
});
