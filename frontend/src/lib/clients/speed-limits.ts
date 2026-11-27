import { HttpUtil } from '@/utils';

export interface SpeedLimit {
  clientId: number;
  email: string;
  uploadBytesPerSecond: number;
  downloadBytesPerSecond: number;
}

export interface SpeedLimitRate {
  upload: number;
  download: number;
}

export const unlimitedSpeedLimit: SpeedLimitRate = { upload: 0, download: 0 };

export function speedLimitRate(row?: SpeedLimit): SpeedLimitRate {
  return row
    ? {
        upload: row.uploadBytesPerSecond / 1_000_000,
        download: row.downloadBytesPerSecond / 1_000_000,
      }
    : unlimitedSpeedLimit;
}

const speedLimitsPath = (id: number) => `/panel/api/inbounds/${id}/speedLimits`;

export function supportsSpeedLimits(protocol: string | undefined, nodeId?: number | null): boolean {
  return (
    !nodeId &&
    ['vless', 'vmess', 'trojan', 'shadowsocks', 'hysteria', 'masque'].includes(protocol ?? '')
  );
}

export function getSpeedLimits(id: number) {
  return HttpUtil.get<SpeedLimit[]>(speedLimitsPath(id), undefined, { silent: true });
}

export function saveSpeedLimit(id: number, clientId: number, rate: SpeedLimitRate) {
  return HttpUtil.post<SpeedLimit[]>(
    speedLimitsPath(id),
    {
      clientId,
      uploadBytesPerSecond: Math.round(rate.upload * 1_000_000),
      downloadBytesPerSecond: Math.round(rate.download * 1_000_000),
    },
    { silent: true, headers: { 'Content-Type': 'application/json' } },
  );
}
