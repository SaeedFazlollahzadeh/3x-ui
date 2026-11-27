import { useEffect, useState } from 'react';
import { Alert, Button, Form, Modal, Select, Space } from 'antd';
import { useTranslation } from 'react-i18next';
import SpeedLimitFields from '@/components/clients/SpeedLimitFields';
import {
  getSpeedLimits,
  saveSpeedLimit,
  speedLimitRate,
  unlimitedSpeedLimit,
  type SpeedLimit,
} from '@/lib/clients/speed-limits';

interface Props {
  id: number;
  clientEmail?: string;
  label?: string;
  zIndex?: number;
}

export default function InboundSpeedLimits({ id, clientEmail, label, zIndex }: Props) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [rows, setRows] = useState<SpeedLimit[]>([]);
  const [selected, setSelected] = useState(0);
  const [rate, setRate] = useState(unlimitedSpeedLimit);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const title = t('pages.inbounds.speedLimits');

  useEffect(() => {
    if (!open) return;
    let active = true;
    void getSpeedLimits(id)
      .then((result) => {
        if (!active) return;
        if (result?.success && result.obj) {
          const options = clientEmail
            ? result.obj.filter(
                (row) => row.email.toLowerCase() === clientEmail.toLowerCase() && row.clientId > 0,
              )
            : result.obj;
          if (!options.length) {
            setError(t('pages.inbounds.speedLimitError'));
            return;
          }
          setRows(options);
          setSelected(options[0].clientId);
          setRate(speedLimitRate(options[0]));
        } else {
          setError(result?.msg || t('pages.inbounds.speedLimitError'));
        }
      })
      .catch(() => {
        if (active) setError(t('pages.inbounds.speedLimitError'));
      })
      .finally(() => {
        if (active) setPending(false);
      });
    return () => {
      active = false;
    };
  }, [open, id, clientEmail, t]);

  function openDialog() {
    setPending(true);
    setError('');
    setRows([]);
    setSelected(0);
    setRate(unlimitedSpeedLimit);
    setOpen(true);
  }

  async function save() {
    setPending(true);
    setError('');
    try {
      const result = await saveSpeedLimit(id, selected, rate);
      if (result?.success) {
        setOpen(false);
      } else {
        setError(result?.msg || t('pages.inbounds.speedLimitError'));
      }
    } catch {
      setError(t('pages.inbounds.speedLimitError'));
    } finally {
      setPending(false);
    }
  }

  return (
    <>
      {label ? (
        <Button onClick={openDialog}>{label}</Button>
      ) : (
        <Form.Item label={title}>
          <Button onClick={openDialog}>{title}</Button>
        </Form.Item>
      )}
      <Modal
        open={open}
        title={label ? `${title} · ${label}` : title}
        zIndex={zIndex}
        onCancel={() => setOpen(false)}
        onOk={() => void save()}
        confirmLoading={pending}
        okButtonProps={{ disabled: !rows.length }}
        mask={{ closable: false }}
      >
        <Space orientation="vertical" style={{ width: '100%' }}>
          <Alert type="info" title={t('pages.inbounds.speedLimitHelp')} />
          {error && <Alert type="error" title={error} />}
          {!clientEmail && (
            <Select
              aria-label={t('pages.inbounds.speedLimitScope')}
              showSearch={{ optionFilterProp: 'label' }}
              style={{ width: '100%' }}
              disabled={pending || !rows.length}
              value={selected}
              options={rows.map((row) => ({
                value: row.clientId,
                label: row.clientId ? row.email : t('pages.inbounds.speedLimitWholeInbound'),
              }))}
              onChange={(clientId) => {
                setSelected(clientId);
                setRate(speedLimitRate(rows.find((row) => row.clientId === clientId)));
              }}
            />
          )}
          <SpeedLimitFields value={rate} onChange={setRate} disabled={pending || !rows.length} />
        </Space>
      </Modal>
    </>
  );
}
