import type { ReactNode } from 'react'

/**
 * 图标集：24px 描边图标，每个导航项使用独立图标，不得复用。
 * 新增图标时在 paths 中补一条，并保持 1.8 描边、圆角端点。
 */
const paths = {
  // 导航
  home: <path d="M3 10.5 12 3l9 7.5V20a1 1 0 0 1-1 1h-5v-6H9v6H4a1 1 0 0 1-1-1z" />,
  key: (
    <>
      <circle cx="8" cy="15" r="4" />
      <path d="m11 12 9-9m-3 3 3 3m-6 0 2 2" />
    </>
  ),
  list: <path d="M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01" />,
  store: (
    <path d="M3 9 4.5 4h15L21 9M3 9h18v2a3 3 0 0 1-6 0 3 3 0 0 1-6 0 3 3 0 0 1-6 0zM5 13v7h14v-7" />
  ),
  server: (
    <>
      <rect x="3" y="4" width="18" height="7" rx="2" />
      <rect x="3" y="13" width="18" height="7" rx="2" />
      <path d="M7 7.5h.01M7 16.5h.01" />
    </>
  ),
  chart: <path d="M4 20V10m6 10V4m6 16v-7m4 7H2" />,
  wallet: (
    <>
      <path d="M20 7H5a2 2 0 0 1 0-4h13v4M3 5v14a2 2 0 0 0 2 2h15V7" />
      <path d="M16 14h.01" />
    </>
  ),
  swap: <path d="M7 4 3 8l4 4M3 8h14M17 20l4-4-4-4m4 4H7" />,
  // 管理后台
  gauge: (
    <>
      <path d="M4 18a9 9 0 1 1 16 0" />
      <path d="m12 14 4-5" />
    </>
  ),
  users: (
    <>
      <circle cx="9" cy="8" r="3" />
      <path d="M3 20c.4-4 2.4-6 6-6s5.6 2 6 6M15 6a3 3 0 0 1 0 6M17 14c2.4.7 3.7 2.7 4 6" />
    </>
  ),
  layers: <path d="m12 3 9 5-9 5-9-5zM3 13l9 5 9-5M3 17.5l9 5 9-5" />,
  shield: (
    <>
      <path d="M12 3 5 6v6c0 4.2 2.9 7.4 7 9 4.1-1.6 7-4.8 7-9V6z" />
      <path d="m9 12 2 2 4-4" />
    </>
  ),
  scale: (
    <path d="M12 4v16M7 20h10M5 7h14M5 7l-3 7a3 3 0 0 0 6 0zM19 7l-3 7a3 3 0 0 0 6 0z" />
  ),
  coins: (
    <>
      <circle cx="9" cy="9" r="6" />
      <path d="M15.5 6.3A6 6 0 1 1 8.3 15" />
      <path d="M9 7v4m-1.5-3h2.2" />
    </>
  ),
  // 通用
  account: (
    <>
      <circle cx="12" cy="8" r="3" />
      <path d="M5 20c.5-4 2.8-6 7-6s6.5 2 7 6" />
    </>
  ),
  settings: (
    <>
      <circle cx="12" cy="12" r="3" />
      <path d="M12 3v2M12 19v2M3 12h2M19 12h2M5.6 5.6 7 7M17 17l1.4 1.4M18.4 5.6 17 7M7 17l-1.4 1.4" />
    </>
  ),
  'arrow-left': <path d="m15 18-6-6 6-6" />,
  'chevron-right': <path d="m9 6 6 6-6 6" />,
  'chevron-down': <path d="m6 9 6 6 6-6" />,
  'arrow-up': <path d="M12 19V5m-6 6 6-6 6 6" />,
  'arrow-down': <path d="M12 5v14m-6-6 6 6 6-6" />,
  grip: <path d="M9 6h.01M15 6h.01M9 12h.01M15 12h.01M9 18h.01M15 18h.01" />,
  trash: <path d="M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13M10 11v6M14 11v6" />,
  live: (
    <>
      <circle cx="12" cy="12" r="2" />
      <path d="M7.8 7.8a6 6 0 0 0 0 8.4M16.2 7.8a6 6 0 0 1 0 8.4M5 5a10 10 0 0 0 0 14M19 5a10 10 0 0 1 0 14" />
    </>
  ),
  download: <path d="M12 4v11m-5-5 5 5 5-5M5 20h14" />,
  back: <path d="M9 14 4 9l5-5M4 9h11a5 5 0 0 1 0 10h-3" />,
  check: <path d="m5 12 4 4L19 6" />,
  x: <path d="M6 6l12 12M18 6 6 18" />,
  copy: (
    <>
      <rect x="9" y="9" width="10" height="10" rx="2" />
      <path d="M15 9V7a2 2 0 0 0-2-2H7a2 2 0 0 0-2 2v6a2 2 0 0 0 2 2h2" />
    </>
  ),
  eye: (
    <>
      <path d="M3 12s3.5-6 9-6 9 6 9 6-3.5 6-9 6-9-6-9-6Z" />
      <circle cx="12" cy="12" r="2.5" />
    </>
  ),
  'eye-off': (
    <path d="m3 3 18 18M10.6 10.7a2 2 0 0 0 2.7 2.7M9.9 4.2A10.5 10.5 0 0 1 12 4c5.5 0 9 6 9 6a15 15 0 0 1-2.2 2.9M6.2 6.2C3.9 7.7 3 10 3 10s3.5 6 9 6a9.8 9.8 0 0 0 3.7-.7" />
  ),
  logout: <path d="M10 5H5v14h5M14 8l4 4-4 4M18 12H9" />,
  menu: <path d="M4 7h16M4 12h16M4 17h16" />,
  more: <path d="M5 12h.01M12 12h.01M19 12h.01" />,
  plus: <path d="M12 5v14M5 12h14" />,
  search: (
    <>
      <circle cx="11" cy="11" r="6" />
      <path d="m16 16 4 4" />
    </>
  ),
  refresh: <path d="M20 11a8 8 0 0 0-14.3-4M4 4v4h4M4 13a8 8 0 0 0 14.3 4M20 20v-4h-4" />,
  alert: <path d="M12 4 2.5 20h19zM12 10v4m0 3h.01" />,
  info: (
    <>
      <circle cx="12" cy="12" r="9" />
      <path d="M12 11v5m0-8h.01" />
    </>
  ),
} satisfies Record<string, ReactNode>

export type IconName = keyof typeof paths

export const iconNames = Object.keys(paths) as IconName[]

export function Icon({ name, size = 18 }: { name: IconName; size?: number }) {
  return (
    <svg
      aria-hidden="true"
      className="icon"
      fill="none"
      height={size}
      viewBox="0 0 24 24"
      width={size}
    >
      <g
        stroke="currentColor"
        strokeLinecap="round"
        strokeLinejoin="round"
        strokeWidth="1.8"
      >
        {paths[name]}
      </g>
    </svg>
  )
}
