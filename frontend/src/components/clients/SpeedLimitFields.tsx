import { useId } from 'react';
import { InputNumber } from 'antd';
import { useTranslation } from 'react-i18next';

import type { SpeedLimitRate } from '@/lib/clients/speed-limits';
import './SpeedLimitFields.css';

export default function SpeedLimitFields({
  value,
  onChange,
  disabled,
}: {
  value: SpeedLimitRate;
  onChange: (rate: SpeedLimitRate) => void;
  disabled?: boolean;
}) {
  const { t } = useTranslation();
  const id = useId();
  return (
    <div className="speed-limit-fields">
      <div>
        <label htmlFor={`${id}-upload`}>{t('pages.inbounds.speedLimitUpload')}</label>
        <InputNumber
          className="speed-limit-input"
          size="large"
          id={`${id}-upload`}
          min={0}
          max={1000}
          step={0.01}
          precision={2}
          suffix="MB/s"
          value={value.upload}
          disabled={disabled}
          onChange={(upload) => onChange({ ...value, upload: upload ?? 0 })}
        />
      </div>
      <div>
        <label htmlFor={`${id}-download`}>{t('pages.inbounds.speedLimitDownload')}</label>
        <InputNumber
          className="speed-limit-input"
          size="large"
          id={`${id}-download`}
          min={0}
          max={1000}
          step={0.01}
          precision={2}
          suffix="MB/s"
          value={value.download}
          disabled={disabled}
          onChange={(download) => onChange({ ...value, download: download ?? 0 })}
        />
      </div>
    </div>
  );
}
