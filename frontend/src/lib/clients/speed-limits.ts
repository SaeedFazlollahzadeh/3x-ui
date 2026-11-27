import { HttpUtil } from '@/utils';

export interface SpeedLimit {
  clientId: number;
  email: string;
  bytesPerSecond: number;
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

export function saveSpeedLimit(id: number, clientId: number, rate: number) {
  return HttpUtil.post<SpeedLimit[]>(
    speedLimitsPath(id),
    { clientId, bytesPerSecond: Math.round(rate * 1_000_000) },
    { silent: true, headers: { 'Content-Type': 'application/json' } },
  );
}
