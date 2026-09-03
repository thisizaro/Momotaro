// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, render, screen } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { LlmQuotaBanner } from '@/components/LlmQuotaBanner';

afterEach(cleanup);

/**
 * Surfaces llm_quota_exhausted_count (docs/API_GATEWAY.md, Unit AI) as a
 * quiet stat, not an alarm: a free-tier quota being spent is a normal
 * operating condition, so this renders nothing at all when there is
 * nothing to say, and a slate-toned note, never red or amber, when there
 * is.
 */
describe('LlmQuotaBanner', () => {
  it('renders nothing when the count is zero', () => {
    const { container } = render(<LlmQuotaBanner count={0} />);
    expect(container.firstChild).toBeNull();
  });

  it('states the count and that records fell back to deterministic rules', () => {
    render(<LlmQuotaBanner count={12} />);
    expect(screen.getByText(/12 records fell back to deterministic rules/)).toBeInTheDocument();
  });

  it('uses the singular for a count of exactly one', () => {
    render(<LlmQuotaBanner count={1} />);
    expect(screen.getByText(/1 record fell back to deterministic rules/)).toBeInTheDocument();
  });
});
