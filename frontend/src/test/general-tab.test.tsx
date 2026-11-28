import { act, fireEvent, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { describe, expect, it, vi } from 'vitest';

import { AllSetting } from '@/models/setting';
import GeneralTab from '@/pages/settings/GeneralTab';
import { HttpUtil } from '@/utils';
import { renderWithProviders } from './test-utils';

// Mounting fetches inbound options with no visible change; settle it inside act().
async function renderGeneralTab(updateSetting: (patch: Partial<AllSetting>) => void) {
  renderWithProviders(
    <MemoryRouter initialEntries={['/settings']}>
      <GeneralTab allSetting={new AllSetting({ pageSize: 25 })} updateSetting={updateSetting} />
    </MemoryRouter>,
  );
  await act(async () => {});
}

describe('GeneralTab', () => {
  it('keeps the stored page size when the field is cleared', async () => {
    const updateSetting = vi.fn();

    await renderGeneralTab(updateSetting);

    const pageSizeInput = screen.getByDisplayValue('25');
    fireEvent.change(pageSizeInput, { target: { value: '' } });
    fireEvent.blur(pageSizeInput);

    expect(updateSetting).not.toHaveBeenCalled();
    expect((pageSizeInput as HTMLInputElement).value).toBe('25');
  });

  it('forwards typed page sizes unchanged, zero included', async () => {
    const updateSetting = vi.fn();

    await renderGeneralTab(updateSetting);

    fireEvent.change(screen.getByDisplayValue('25'), { target: { value: '0' } });

    expect(updateSetting).toHaveBeenCalledWith({ pageSize: 0 });
  });
  it('saves separate retention and backup day counts', async () => {
    const updateSetting = vi.fn();
    await renderGeneralTab(updateSetting);
    fireEvent.click(screen.getByRole('tab', { name: 'Activity logs' }));
    fireEvent.change(screen.getByLabelText('Keep access logs (days)'), { target: { value: '7' } });
    fireEvent.change(screen.getByLabelText('Back up subscription login logs (days)'), {
      target: { value: '30' },
    });
    expect(updateSetting).toHaveBeenCalledWith({ accessLogRetentionDays: 7 });
    expect(updateSetting).toHaveBeenCalledWith({ subscriptionLogBackupDays: 30 });
  });
  it('enables Telegram and email nightly backup delivery independently', async () => {
    const updateSetting = vi.fn();
    await renderGeneralTab(updateSetting);
    fireEvent.click(screen.getByRole('tab', { name: 'Activity logs' }));
    fireEvent.click(screen.getByRole('switch', { name: 'Send nightly log backups by Telegram' }));
    fireEvent.click(screen.getByRole('switch', { name: 'Send nightly log backups by email' }));
    expect(updateSetting.mock.calls).toEqual([
      [{ activityLogTelegramBackup: true }],
      [{ activityLogEmailBackup: true }],
    ]);
  });
  it('generates a manual backup using the saved settings', async () => {
    await renderGeneralTab(vi.fn());
    fireEvent.click(screen.getByRole('tab', { name: 'Activity logs' }));
    const post = vi.spyOn(HttpUtil, 'post');
    try {
      await act(async () => {
        fireEvent.click(screen.getByRole('button', { name: 'Manual log backup' }));
      });
      expect(post).toHaveBeenCalledWith('/panel/api/setting/activityBackup', undefined, {
        timeout: 180_000,
      });
    } finally {
      post.mockRestore();
    }
  });
});
