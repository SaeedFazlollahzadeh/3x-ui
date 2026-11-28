import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import {
  Alert,
  Button,
  Card,
  ConfigProvider,
  DatePicker,
  Form,
  Input,
  Layout,
  Select,
  Space,
  Table,
  Typography,
} from 'antd';
import type { Dayjs } from 'dayjs';

import type {
  AccessLogEntry,
  AccessLogPage,
  SubscriptionLogEntry,
  SubscriptionLogPage,
} from '@/generated/types';
import { useTheme } from '@/hooks/useTheme';
import AppSidebar from '@/layouts/AppSidebar';
import { HttpUtil } from '@/utils';
import './ActivityLogsPage.css';

interface Filters {
  client?: string;
  inbound?: string;
  search?: string;
  status?: string;
  from?: number;
  to?: number;
}

interface FilterFields extends Omit<Filters, 'from' | 'to'> {
  from?: Dayjs;
  to?: Dayjs;
}

async function fetchLogs(
  kind: 'access' | 'subscription',
  filters: Filters,
  before: number,
  signal: AbortSignal,
) {
  const params = { ...filters, before };
  if (kind === 'access') {
    const msg = await HttpUtil.get<AccessLogPage>('/panel/api/activity/access', params, {
      silent: true,
      signal,
    });
    if (!msg.success || !msg.obj) throw new Error(msg.msg);
    return { kind, ...msg.obj } as const;
  }
  const msg = await HttpUtil.get<SubscriptionLogPage>('/panel/api/activity/subscription', params, {
    silent: true,
    signal,
  });
  if (!msg.success || !msg.obj) throw new Error(msg.msg);
  return { kind, ...msg.obj } as const;
}

function ActivityLogsPage({ kind }: { kind: 'access' | 'subscription' }) {
  const { t, i18n } = useTranslation();
  const { antdThemeConfig } = useTheme();
  const [form] = Form.useForm<FilterFields>();
  const [filters, setFilters] = useState<Filters>({});
  const [cursors, setCursors] = useState([0]);
  const before = cursors[cursors.length - 1];
  const { data, isFetching, isError, refetch } = useQuery({
    queryKey: ['activityLogs', kind, filters, before],
    queryFn: ({ signal }) => fetchLogs(kind, filters, before, signal),
  });
  const isAccess = kind === 'access';
  const formatDate = (at: number | string) => new Date(at).toLocaleString(i18n.language);
  const text = (value: string) => value || '—';

  function applyFilters(values: FilterFields) {
    setFilters({
      client: values.client?.trim(),
      inbound: values.inbound?.trim(),
      search: values.search?.trim(),
      status: values.status,
      from: values.from?.startOf('day').valueOf(),
      to: values.to?.startOf('day').add(1, 'day').valueOf(),
    });
    setCursors([0]);
  }

  return (
    <ConfigProvider theme={antdThemeConfig}>
      <Layout className="activity-logs-page">
        <AppSidebar />
        <Layout className="content-shell">
          <Layout.Content id="content-layout" className="content-area">
            <Card title={t(isAccess ? 'menu.clientAccessLogs' : 'menu.subscriptionLoginLogs')}>
              <Typography.Paragraph type="secondary">
                {t(
                  isAccess
                    ? 'pages.activityLogs.accessHelp'
                    : 'pages.activityLogs.subscriptionHelp',
                )}
              </Typography.Paragraph>
              <Form
                form={form}
                layout="vertical"
                onFinish={applyFilters}
                className="activity-log-filters"
              >
                <Form.Item
                  name="client"
                  label={t(
                    isAccess ? 'pages.activityLogs.clientNameFilter' : 'pages.activityLogs.client',
                  )}
                >
                  <Input allowClear maxLength={256} />
                </Form.Item>
                <Form.Item
                  name="inbound"
                  label={t(
                    isAccess
                      ? 'pages.activityLogs.inboundNameFilter'
                      : 'pages.activityLogs.inboundMembership',
                  )}
                >
                  <Input allowClear maxLength={256} />
                </Form.Item>
                <Form.Item
                  name="search"
                  label={t(
                    isAccess
                      ? 'pages.activityLogs.destinationOrIp'
                      : 'pages.activityLogs.deviceOrIp',
                  )}
                >
                  <Input allowClear maxLength={256} />
                </Form.Item>
                {isAccess && (
                  <Form.Item name="status" label={t('pages.activityLogs.status')}>
                    <Select
                      allowClear
                      options={[
                        { value: 'accepted', label: t('pages.activityLogs.accepted') },
                        { value: 'rejected', label: t('pages.activityLogs.rejected') },
                      ]}
                    />
                  </Form.Item>
                )}
                <Form.Item name="from" label={t('pages.activityLogs.from')}>
                  <DatePicker />
                </Form.Item>
                <Form.Item
                  name="to"
                  label={t('pages.activityLogs.to')}
                  dependencies={['from']}
                  rules={[
                    ({ getFieldValue }) => ({
                      validator: (_, value?: Dayjs) => {
                        const from: Dayjs | undefined = getFieldValue('from');
                        return value && from && value.isBefore(from, 'day')
                          ? Promise.reject(new Error(t('pages.activityLogs.invalidDates')))
                          : Promise.resolve();
                      },
                    }),
                  ]}
                >
                  <DatePicker />
                </Form.Item>
                <Space className="activity-log-actions" wrap>
                  <Button type="primary" htmlType="submit">
                    {t('filter')}
                  </Button>
                  <Button
                    onClick={() => {
                      form.resetFields();
                      setFilters({});
                      setCursors([0]);
                    }}
                  >
                    {t('clear')}
                  </Button>
                </Space>
              </Form>

              {isError && (
                <Alert
                  type="error"
                  title={t('pages.activityLogs.loadError')}
                  action={<Button onClick={() => void refetch()}>{t('refresh')}</Button>}
                />
              )}
              {data?.kind === 'access' && !data.enabled && (
                <Alert type="info" title={t('pages.clients.noClientAccessLogs')} />
              )}

              {isAccess ? (
                <Table<AccessLogEntry>
                  rowKey="offset"
                  loading={isFetching}
                  dataSource={data?.kind === 'access' ? data.rows : []}
                  pagination={false}
                  scroll={{ x: 860 }}
                  locale={{ emptyText: t('pages.activityLogs.noMatches') }}
                  columns={[
                    {
                      title: t('pages.clients.activityTime'),
                      dataIndex: 'DateTime',
                      render: formatDate,
                      width: 190,
                    },
                    {
                      title: t('pages.activityLogs.clientName'),
                      dataIndex: 'Email',
                      render: (email: string, row) => text(row.ClientName || email),
                    },
                    {
                      title: t('pages.clients.activityInbound'),
                      dataIndex: 'Inbound',
                      render: (tag: string, row) => text(row.InboundName || tag),
                    },
                    {
                      title: t('pages.activityLogs.sourceAddress'),
                      dataIndex: 'FromAddress',
                      render: text,
                    },
                    {
                      title: t('pages.clients.activityDestination'),
                      dataIndex: 'ToAddress',
                      render: text,
                    },
                    {
                      title: t('pages.activityLogs.status'),
                      dataIndex: 'status',
                      render: (status: AccessLogEntry['status']) =>
                        t(`pages.activityLogs.${status}`),
                    },
                  ]}
                />
              ) : (
                <Table<SubscriptionLogEntry>
                  rowKey="id"
                  loading={isFetching}
                  dataSource={data?.kind === 'subscription' ? data.rows : []}
                  pagination={false}
                  scroll={{ x: 640 }}
                  locale={{ emptyText: t('pages.activityLogs.noMatches') }}
                  columns={[
                    {
                      title: t('pages.clients.activityTime'),
                      dataIndex: 'at',
                      render: formatDate,
                      width: 190,
                    },
                    { title: t('pages.activityLogs.client'), dataIndex: 'email', render: text },
                    { title: 'IP', dataIndex: 'ip', render: text, width: 160 },
                    {
                      title: t('pages.clients.reportedDevice'),
                      dataIndex: 'userAgent',
                      render: text,
                    },
                  ]}
                />
              )}
              <Space className="activity-log-pagination" wrap>
                <Button
                  disabled={cursors.length <= 1 || isFetching}
                  onClick={() => setCursors((pages) => pages.slice(0, -1))}
                >
                  {t('pages.activityLogs.newer')}
                </Button>
                <Button
                  disabled={!data?.nextBefore || isFetching}
                  onClick={() => {
                    if (data?.nextBefore) setCursors((pages) => [...pages, data.nextBefore]);
                  }}
                >
                  {t('pages.activityLogs.older')}
                </Button>
                <Button
                  onClick={() => {
                    if (before) setCursors([0]);
                    else void refetch();
                  }}
                  loading={isFetching}
                >
                  {t('refresh')}
                </Button>
              </Space>
            </Card>
          </Layout.Content>
        </Layout>
      </Layout>
    </ConfigProvider>
  );
}

export function AccessLogsPage() {
  return <ActivityLogsPage kind="access" />;
}

export function SubscriptionLoginLogsPage() {
  return <ActivityLogsPage kind="subscription" />;
}
