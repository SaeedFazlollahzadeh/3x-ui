import { fireEvent, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, onTestFinished, vi } from 'vitest';

import { AccessLogsPage, SubscriptionLoginLogsPage } from '@/pages/activity/ActivityLogsPage';
import { HttpUtil } from '@/utils';
import { renderWithProviders } from './test-utils';

vi.mock('@/layouts/AppSidebar', () => ({ default: () => null }));

describe('activity log pages', () => {
  it('filters access logs on the server and requests older matching rows', async () => {
    const response = {
      success: true,
      msg: '',
      obj: { rows: [], enabled: true, nextBefore: 0 },
    };
    const get = vi
      .spyOn(HttpUtil, 'get')
      .mockResolvedValueOnce(response)
      .mockResolvedValueOnce({ ...response, obj: { ...response.obj, nextBefore: 150 } })
      .mockResolvedValueOnce(response);
    onTestFinished(() => get.mockRestore());

    renderWithProviders(<AccessLogsPage />);
    await screen.findByText('No matching logs.');
    fireEvent.change(screen.getByLabelText('Client name or email'), { target: { value: 'alice' } });
    fireEvent.change(screen.getByLabelText('Inbound name or tag'), {
      target: { value: 'inbound-443' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Filter' }));
    await waitFor(() =>
      expect(get).toHaveBeenCalledWith(
        '/panel/api/activity/access',
        expect.objectContaining({ client: 'alice', inbound: 'inbound-443', before: 0 }),
        expect.objectContaining({ silent: true }),
      ),
    );
    await waitFor(() =>
      expect(screen.getByRole<HTMLButtonElement>('button', { name: 'Older' }).disabled).toBe(false),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Older' }));
    await waitFor(() =>
      expect(get).toHaveBeenCalledWith(
        '/panel/api/activity/access',
        expect.objectContaining({ client: 'alice', inbound: 'inbound-443', before: 150 }),
        expect.objectContaining({ silent: true }),
      ),
    );
  });

  it('renders subscription downloads with client email and device', async () => {
    const get = vi.spyOn(HttpUtil, 'get').mockResolvedValue({
      success: true,
      msg: '',
      obj: {
        rows: [
          {
            id: 9,
            email: 'alice@example.com',
            at: 1735689600000,
            ip: '192.0.2.8',
            userAgent: 'Example VPN',
          },
        ],
        nextBefore: 0,
      },
    });
    onTestFinished(() => get.mockRestore());

    renderWithProviders(<SubscriptionLoginLogsPage />);
    expect(await screen.findByText('alice@example.com')).toBeTruthy();
    expect(screen.getByText('Example VPN')).toBeTruthy();
    expect(screen.getByLabelText('Attached inbound')).toBeTruthy();
  });
});
