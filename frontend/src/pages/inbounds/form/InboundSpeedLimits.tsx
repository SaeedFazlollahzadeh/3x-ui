import { useEffect, useState } from 'react';
import { Alert, Button, Form, InputNumber, Modal, Select, Space } from 'antd';
import { useTranslation } from 'react-i18next';
import { HttpUtil } from '@/utils';

interface SpeedLimit {
  clientId: number;
  email: string;
  bytesPerSecond: number;
}

export default function InboundSpeedLimits({ id }: { id: number }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [rows, setRows] = useState<SpeedLimit[]>([]);
  const [selected, setSelected] = useState(0);
  const [rate, setRate] = useState<number | null>(0);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const path = `/panel/api/inbounds/${id}/speedLimits`;
  const title = t('pages.inbounds.speedLimits');

  useEffect(() => {
    if (!open) return;
    let active = true;
    void HttpUtil.get<SpeedLimit[]>(path, undefined, { silent: true })
      .then((result) => {
        if (!active) return;
        if (result?.success && result.obj) {
          setRows(result.obj);
          setRate((result.obj[0]?.bytesPerSecond ?? 0) / 1_000_000);
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
  }, [open, path, t]);

  function openDialog() {
    setPending(true);
    setError('');
    setRows([]);
    setSelected(0);
    setRate(0);
    setOpen(true);
  }

  async function save() {
    setPending(true);
    setError('');
    try {
      const result = await HttpUtil.post<SpeedLimit[]>(
        path,
        { clientId: selected, bytesPerSecond: Math.round((rate ?? 0) * 1_000_000) },
        { silent: true },
      );
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
      <Form.Item label={title}>
        <Button onClick={openDialog}>{title}</Button>
      </Form.Item>
      <Modal
        open={open}
        title={title}
        onCancel={() => setOpen(false)}
        onOk={() => void save()}
        confirmLoading={pending}
        okButtonProps={{ disabled: !rows.length }}
        mask={{ closable: false }}
      >
        <Space orientation="vertical" style={{ width: '100%' }}>
          <Alert type="info" title={t('pages.inbounds.speedLimitHelp')} />
          {error && <Alert type="error" title={error} />}
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
              setRate(
                (rows.find((row) => row.clientId === clientId)?.bytesPerSecond ?? 0) / 1_000_000,
              );
            }}
          />
          <InputNumber
            aria-label={t('pages.inbounds.speedLimitRate')}
            suffix="MB/s"
            min={0}
            max={1000}
            step={0.01}
            value={rate}
            disabled={pending || !rows.length}
            onChange={setRate}
          />
        </Space>
      </Modal>
    </>
  );
}
