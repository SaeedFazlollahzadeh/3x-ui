import { useState } from 'react';
import { Typography } from 'antd';
import { useTranslation } from 'react-i18next';
import { SizeFormatter } from '@/utils';

export interface DailyTraffic {
  day: string;
  up: number;
  down: number;
}

export default function DailyTrafficChart({ days }: { days: DailyTraffic[] }) {
  const { t } = useTranslation();
  const [selectedDay, setSelectedDay] = useState<string>();
  if (!days.length) return null;

  const upload = t('pages.clients.upload');
  const download = t('pages.clients.download');
  const peak = Math.max(1, ...days.flatMap(({ up, down }) => [up, down]));
  const selected = days.find(({ day }) => day === selectedDay) ?? days[days.length - 1];
  const dayLabel = ({ day, up, down }: DailyTraffic) =>
    `${day} · ${upload} ${SizeFormatter.sizeFormat(up)} · ${download} ${SizeFormatter.sizeFormat(down)}`;
  return (
    <section>
      <Typography.Title level={5}>{t('pages.clients.dailyTraffic')}</Typography.Title>
      <Typography.Text style={{ color: 'var(--ant-color-primary, #1677ff)' }}>
        ■ {upload}
      </Typography.Text>{' '}
      <Typography.Text style={{ color: 'var(--ant-color-success, #237804)' }}>
        ■ {download}
      </Typography.Text>
      <div style={{ overflowX: 'auto' }}>
        <div style={{ display: 'flex', minWidth: days.length * 16, gap: 2, marginBlock: 8 }}>
          {days.map((row) => {
            const { day, up, down } = row;
            const label = dayLabel(row);
            return (
              <button
                type="button"
                key={day}
                title={label}
                aria-label={label}
                aria-pressed={day === selected.day}
                onClick={() => setSelectedDay(day)}
                style={{
                  flex: 1,
                  cursor: 'pointer',
                  padding: 0,
                  border: 0,
                  background:
                    day === selected.day ? 'var(--ant-color-fill-secondary, #8882)' : 'transparent',
                  display: 'flex',
                  gap: 1,
                  alignItems: 'end',
                  height: 100,
                  borderBottom: '1px solid var(--ant-color-border, #999)',
                }}
              >
                <div
                  style={{
                    flex: 1,
                    height: `${(up / peak) * 100}%`,
                    background: 'var(--ant-color-primary, #1677ff)',
                  }}
                />
                <div
                  style={{
                    flex: 1,
                    height: `${(down / peak) * 100}%`,
                    background: 'var(--ant-color-success, #237804)',
                  }}
                />
              </button>
            );
          })}
        </div>
      </div>
      <div style={{ display: 'flex', justifyContent: 'space-between' }}>
        <Typography.Text type="secondary">{days[0].day} UTC</Typography.Text>
        <Typography.Text type="secondary">{days[days.length - 1].day} UTC</Typography.Text>
      </div>
      <Typography.Text aria-live="polite">{dayLabel(selected)}</Typography.Text>
    </section>
  );
}
