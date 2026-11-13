import React, { useId } from 'react';
import { FileCategory } from '../types';

/**
 * Custom precision geometric brand emblem for mytgfs
 */
export const BrandEmblemSvg: React.FC<{ className?: string }> = ({ className = 'w-8 h-8' }) => {
  const uid = useId().replace(/:/g, '');
  const gradId = `brand-grad-${uid}`;
  const innerId = `brand-inner-${uid}`;

  return (
    <svg viewBox="0 0 40 40" fill="none" className={className} aria-hidden="true">
      <defs>
        <linearGradient id={gradId} x1="0" y1="0" x2="40" y2="40" gradientUnits="userSpaceOnUse">
          <stop offset="0%" stopColor="#007AFF" />
          <stop offset="55%" stopColor="#0284C7" />
          <stop offset="100%" stopColor="#06B6D4" />
        </linearGradient>
        <linearGradient id={innerId} x1="8" y1="8" x2="32" y2="32" gradientUnits="userSpaceOnUse">
          <stop offset="0%" stopColor="#FFFFFF" stopOpacity="0.98" />
          <stop offset="100%" stopColor="#E0F2FE" stopOpacity="0.9" />
        </linearGradient>
      </defs>
      <rect width="40" height="40" rx="11" fill={`url(#${gradId})`} />
      <rect
        x="1"
        y="1"
        width="38"
        height="38"
        rx="10"
        stroke="white"
        strokeOpacity="0.25"
        strokeWidth="1"
      />
      {/* Layered vault plates + paper plane vector */}
      <path
        d="M10 14.5C10 12.567 11.567 11 13.5 11H18.2L20.7 13.5H26.5C28.433 13.5 30 15.067 30 17V25.5C30 27.433 28.433 29 26.5 29H13.5C11.567 29 10 27.433 10 25.5V14.5Z"
        fill="#FFFFFF"
        fillOpacity="0.18"
        stroke="white"
        strokeOpacity="0.55"
        strokeWidth="1.4"
      />
      <path
        d="M25.8 16.2L13.6 20.9C13.0 21.15 13.02 21.95 13.62 22.15L16.75 23.15L23.5 18.8L18.35 24.05V26.8C18.35 27.35 19.02 27.6 19.4 27.2L21.15 25.35L23.9 27.4C24.4 27.78 25.12 27.48 25.25 26.85L26.85 17.15C26.98 16.45 26.42 15.95 25.8 16.2Z"
        fill={`url(#${innerId})`}
      />
    </svg>
  );
};

/**
 * Rich macOS/Finder-inspired multi-layered SVG emblem for each file category
 */
export const RichFileTypeEmblem: React.FC<{
  category: FileCategory;
  size?: 'sm' | 'md' | 'lg';
}> = ({ category, size = 'md' }) => {
  const uid = useId().replace(/:/g, '');
  const dims =
    size === 'lg' ? 'w-14 h-14' : size === 'sm' ? 'w-9 h-9' : 'w-11 h-11';

  if (category === 'folder') {
    return (
      <svg viewBox="0 0 48 48" fill="none" className={`${dims} shrink-0`} aria-hidden="true">
        <defs>
          <linearGradient id={`fb-${uid}`} x1="4" y1="8" x2="44" y2="40" gradientUnits="userSpaceOnUse">
            <stop offset="0%" stopColor="#0284C7" />
            <stop offset="100%" stopColor="#0369A1" />
          </linearGradient>
          <linearGradient id={`ff-${uid}`} x1="4" y1="15" x2="44" y2="41" gradientUnits="userSpaceOnUse">
            <stop offset="0%" stopColor="#38BDF8" />
            <stop offset="100%" stopColor="#0284C7" />
          </linearGradient>
        </defs>
        <path
          d="M5 13C5 10.7909 6.79086 9 9 9H18.3431C19.404 9 20.4214 9.42143 21.1716 10.1716L23.8284 12.8284C24.5786 13.5786 25.596 14 26.6569 14H39C41.2091 14 43 15.7909 43 18V36C43 38.2091 41.2091 40 39 40H9C6.79086 40 5 38.2091 5 36V13Z"
          fill={`url(#fb-${uid})`}
        />
        <rect x="8" y="13" width="32" height="6" rx="2" fill="#E0F2FE" fillOpacity="0.65" />
        <path
          d="M4 19C4 16.7909 5.79086 15 8 15H40C42.2091 15 44 16.7909 44 19V36C44 38.7614 41.7614 41 39 41H9C6.23858 41 4 38.7614 4 36V19Z"
          fill={`url(#ff-${uid})`}
        />
        <path
          d="M8 16H40C41.6569 16 43 17.3431 43 19V19.5H5V19C5 17.3431 6.34315 16 8 16Z"
          fill="white"
          fillOpacity="0.28"
        />
      </svg>
    );
  }

  if (category === 'image') {
    return (
      <svg viewBox="0 0 48 48" fill="none" className={`${dims} shrink-0`} aria-hidden="true">
        <defs>
          <linearGradient id={`img-${uid}`} x1="6" y1="6" x2="42" y2="42" gradientUnits="userSpaceOnUse">
            <stop offset="0%" stopColor="#10B981" />
            <stop offset="100%" stopColor="#047857" />
          </linearGradient>
        </defs>
        <rect x="6" y="6" width="36" height="36" rx="10" fill={`url(#img-${uid})`} />
        <circle cx="17.5" cy="17.5" r="3.5" fill="#D1FAE5" />
        <path
          d="M8 34L17.5 24.5C18.6 23.4 20.4 23.4 21.5 24.5L27 30L32.5 24.5C33.6 23.4 35.4 23.4 36.5 24.5L40 28V35C40 37.7614 37.7614 40 35 40H13C10.2386 40 8 37.7614 8 35V34Z"
          fill="#A7F3D0"
          fillOpacity="0.85"
        />
      </svg>
    );
  }

  if (category === 'video') {
    return (
      <svg viewBox="0 0 48 48" fill="none" className={`${dims} shrink-0`} aria-hidden="true">
        <defs>
          <linearGradient id={`vid-${uid}`} x1="6" y1="6" x2="42" y2="42" gradientUnits="userSpaceOnUse">
            <stop offset="0%" stopColor="#F59E0B" />
            <stop offset="100%" stopColor="#B45309" />
          </linearGradient>
        </defs>
        <rect x="6" y="6" width="36" height="36" rx="10" fill={`url(#vid-${uid})`} />
        <path
          d="M20 16.8C20 15.58 21.35 14.84 22.38 15.5L32.58 22.02C33.52 22.62 33.52 24.02 32.58 24.62L22.38 31.14C21.35 31.8 20 31.06 20 29.84V16.8Z"
          fill="#FEF3C7"
        />
        <rect x="11" y="12" width="3" height="4" rx="1" fill="white" fillOpacity="0.45" />
        <rect x="11" y="22" width="3" height="4" rx="1" fill="white" fillOpacity="0.45" />
        <rect x="11" y="32" width="3" height="4" rx="1" fill="white" fillOpacity="0.45" />
      </svg>
    );
  }

  if (category === 'audio') {
    return (
      <svg viewBox="0 0 48 48" fill="none" className={`${dims} shrink-0`} aria-hidden="true">
        <defs>
          <linearGradient id={`aud-${uid}`} x1="6" y1="6" x2="42" y2="42" gradientUnits="userSpaceOnUse">
            <stop offset="0%" stopColor="#8B5CF6" />
            <stop offset="100%" stopColor="#4C1D95" />
          </linearGradient>
        </defs>
        <rect x="6" y="6" width="36" height="36" rx="10" fill={`url(#aud-${uid})`} />
        <rect x="13" y="21" width="3.2" height="10" rx="1.6" fill="#EDE9FE" />
        <rect x="18.5" y="15" width="3.2" height="19" rx="1.6" fill="#DDD6FE" />
        <rect x="24" y="12" width="3.2" height="24" rx="1.6" fill="#FFFFFF" />
        <rect x="29.5" y="17" width="3.2" height="15" rx="1.6" fill="#DDD6FE" />
        <rect x="35" y="22" width="3.2" height="8" rx="1.6" fill="#EDE9FE" />
      </svg>
    );
  }

  if (category === 'pdf') {
    return (
      <svg viewBox="0 0 48 48" fill="none" className={`${dims} shrink-0`} aria-hidden="true">
        <defs>
          <linearGradient id={`pdf-${uid}`} x1="8" y1="5" x2="40" y2="43" gradientUnits="userSpaceOnUse">
            <stop offset="0%" stopColor="#F43F5E" />
            <stop offset="100%" stopColor="#BE123C" />
          </linearGradient>
        </defs>
        <path
          d="M10 9C10 6.79086 11.7909 5 14 5H29.1716C30.2324 5 31.2499 5.42143 32 6.17157L37.8284 12C38.5786 12.7501 39 13.7676 39 14.8284V39C39 41.2091 37.2091 43 35 43H14C11.7909 43 10 41.2091 10 39V9Z"
          fill={`url(#pdf-${uid})`}
        />
        <path d="M29 5.5V12C29 13.6569 30.3431 15 32 15H38.5" fill="#FFE4E6" fillOpacity="0.4" />
        <rect x="15" y="21" width="18" height="2.4" rx="1.2" fill="#FFE4E6" />
        <rect x="15" y="26.5" width="14" height="2.4" rx="1.2" fill="#FFE4E6" fillOpacity="0.8" />
        <rect x="15" y="32" width="10" height="2.4" rx="1.2" fill="#FFE4E6" fillOpacity="0.6" />
      </svg>
    );
  }

  // Archive / Document
  return (
    <svg viewBox="0 0 48 48" fill="none" className={`${dims} shrink-0`} aria-hidden="true">
      <defs>
        <linearGradient id={`arch-${uid}`} x1="6" y1="6" x2="42" y2="42" gradientUnits="userSpaceOnUse">
          <stop offset="0%" stopColor="#475569" />
          <stop offset="100%" stopColor="#1E293B" />
        </linearGradient>
      </defs>
      <rect x="6" y="6" width="36" height="36" rx="10" fill={`url(#arch-${uid})`} />
      <rect x="21" y="9" width="6" height="4" rx="1" fill="#CBD5E1" />
      <rect x="21" y="15" width="6" height="4" rx="1" fill="#94A3B8" />
      <rect x="21" y="21" width="6" height="4" rx="1" fill="#CBD5E1" />
      <rect x="19" y="27" width="10" height="11" rx="2.5" stroke="#E2E8F0" strokeWidth="2.2" />
      <circle cx="24" cy="32.5" r="1.5" fill="#E2E8F0" />
    </svg>
  );
};

/**
 * Radial SVG Storage Ring for Overview Hero Telemetry
 */
export const StorageRadialGauge: React.FC<{
  volumes: { name: string; bytes: number; color: string }[];
  totalLabel: string;
  subLabel: string;
}> = ({ volumes, totalLabel, subLabel }) => {
  const total = Math.max(
    1,
    volumes.reduce((acc, v) => acc + v.bytes, 0)
  );
  const radius = 58;
  const circumference = 2 * Math.PI * radius;
  let cumulativeRatio = 0;

  return (
    <div className="relative w-40 h-40 flex items-center justify-center shrink-0">
      <svg viewBox="0 0 140 140" className="w-full h-full -rotate-90" aria-hidden="true">
        <circle
          cx="70"
          cy="70"
          r={radius}
          fill="none"
          stroke="currentColor"
          strokeWidth="12"
          className="text-zinc-200/80 dark:text-zinc-800/90"
        />
        {volumes.map((v) => {
          const ratio = Math.max(0.03, v.bytes / total);
          const dash = ratio * circumference;
          const offset = -cumulativeRatio * circumference;
          cumulativeRatio += ratio;
          return (
            <circle
              key={v.name}
              cx="70"
              cy="70"
              r={radius}
              fill="none"
              stroke={v.color}
              strokeWidth="12"
              strokeDasharray={`${Math.max(2, dash - 3)} ${circumference}`}
              strokeDashoffset={offset}
              strokeLinecap="round"
            />
          );
        })}
      </svg>
      <div className="absolute inset-0 flex flex-col items-center justify-center text-center px-3">
        <span className="text-lg font-bold font-display tabular-nums text-zinc-950 dark:text-white leading-tight">
          {totalLabel}
        </span>
        <span className="text-[10px] font-mono uppercase tracking-wider text-zinc-500 dark:text-zinc-400 mt-0.5">
          {subLabel}
        </span>
      </div>
    </div>
  );
};

/**
 * Interactive WebDAV Architecture Topology SVG Diagram for MountView
 */
export const WebdavTopologyIllustration: React.FC<{
  enabled: boolean;
  endpointUrl: string;
}> = ({ enabled, endpointUrl }) => (
  <div className="rounded-xl surface-sub p-5 overflow-hidden">
    <svg viewBox="0 0 680 120" fill="none" className="w-full h-auto" aria-hidden="true">
      <defs>
        <linearGradient id="pipe-active" x1="0" y1="0" x2="680" y2="0" gradientUnits="userSpaceOnUse">
          <stop offset="0%" stopColor="#3B82F6" />
          <stop offset="50%" stopColor="#06B6D4" />
          <stop offset="100%" stopColor="#10B981" />
        </linearGradient>
      </defs>

      {/* Connecting bus lines */}
      <line
        x1="185"
        y1="60"
        x2="245"
        y2="60"
        stroke={enabled ? 'url(#pipe-active)' : '#52525B'}
        strokeWidth="2.5"
        strokeDasharray={enabled ? '6 4' : '2 4'}
      />
      <line
        x1="435"
        y1="60"
        x2="495"
        y2="60"
        stroke={enabled ? 'url(#pipe-active)' : '#52525B'}
        strokeWidth="2.5"
        strokeDasharray={enabled ? '6 4' : '2 4'}
      />

      {/* Node 1: Telegram MTProto Private Channels */}
      <g transform="translate(10, 18)">
        <rect
          width="175"
          height="84"
          rx="14"
          fill="#0F172A"
          stroke="#38BDF8"
          strokeOpacity="0.45"
          strokeWidth="1.5"
        />
        <circle cx="34" cy="42" r="16" fill="#0284C7" fillOpacity="0.25" />
        <path
          d="M40 34L26 39.5L30.5 41.5L37.5 36.5L32.5 42.5V46.5L35 44L38.5 46.5L40 34Z"
          fill="#38BDF8"
        />
        <text x="58" y="38" fill="#F8FAFC" fontSize="11" fontWeight="600" fontFamily="Plus Jakarta Sans">
          TG Channels
        </text>
        <text x="58" y="54" fill="#94A3B8" fontSize="9" fontFamily="JetBrains Mono">
          MTProto 2GB Blobs
        </text>
      </g>

      {/* Node 2: mytgfs Local Daemon + SQLite WAL */}
      <g transform="translate(245, 12)">
        <rect
          width="190"
          height="96"
          rx="16"
          fill="#0F172A"
          stroke={enabled ? '#3B82F6' : '#52525B'}
          strokeWidth="2"
        />
        <rect x="16" y="22" width="32" height="52" rx="6" fill="#1E293B" stroke="#60A5FA" strokeWidth="1.4" />
        <line x1="22" y1="33" x2="42" y2="33" stroke="#38BDF8" strokeWidth="2" />
        <line x1="22" y1="43" x2="42" y2="43" stroke="#38BDF8" strokeWidth="2" />
        <circle cx="39" cy="58" r="3" fill={enabled ? '#10B981' : '#EF4444'} />
        <text x="58" y="40" fill="#F8FAFC" fontSize="11.5" fontWeight="700" fontFamily="Plus Jakarta Sans">
          mytgfs Daemon
        </text>
        <text x="58" y="56" fill="#93C5FD" fontSize="9" fontFamily="JetBrains Mono">
          SQLite + Chunk Stream
        </text>
        <text x="58" y="71" fill="#64748B" fontSize="9" fontFamily="JetBrains Mono">
          127.0.0.1 Loopback
        </text>
      </g>

      {/* Node 3: Local OS Native Drive Mount */}
      <g transform="translate(495, 18)">
        <rect
          width="175"
          height="84"
          rx="14"
          fill="#0F172A"
          stroke={enabled ? '#10B981' : '#52525B'}
          strokeOpacity="0.6"
          strokeWidth="1.5"
        />
        <rect x="18" y="26" width="30" height="32" rx="7" fill="#065F46" fillOpacity="0.35" />
        <path
          d="M26 38H40M26 46H36"
          stroke={enabled ? '#34D399' : '#71717A'}
          strokeWidth="2"
          strokeLinecap="round"
        />
        <text x="56" y="38" fill="#F8FAFC" fontSize="11" fontWeight="600" fontFamily="Plus Jakarta Sans">
          OS Native Disk
        </text>
        <text x="56" y="54" fill="#A7F3D0" fontSize="9" fontFamily="JetBrains Mono">
          {enabled ? 'MOUNTED READY' : 'UNMOUNTED'}
        </text>
      </g>
    </svg>
    <div className="mt-2 flex flex-wrap items-center justify-between gap-2 text-[11px] font-mono text-zinc-500 dark:text-zinc-400 px-1">
      <span>STREAM: ENCRYPTED MTPROTO CHUNKS</span>
      <span>ENDPOINT: {endpointUrl}</span>
    </div>
  </div>
);

/**
 * Custom SVG Illustration for Empty States
 */
export const EmptyVaultIllustration: React.FC = () => (
  <svg viewBox="0 0 120 96" fill="none" className="w-28 h-24 mx-auto" aria-hidden="true">
    <rect
      x="16"
      y="18"
      width="88"
      height="62"
      rx="12"
      className="stroke-zinc-300 dark:stroke-zinc-700 fill-zinc-100/60 dark:fill-zinc-900/60"
      strokeWidth="2"
    />
    <path
      d="M36 42H84M36 54H68"
      className="stroke-zinc-300 dark:stroke-zinc-700"
      strokeWidth="2.5"
      strokeLinecap="round"
    />
    <circle
      cx="88"
      cy="66"
      r="16"
      className="fill-blue-500/15 stroke-blue-500"
      strokeWidth="2"
    />
    <path
      d="M88 59V73M81 66H95"
      className="stroke-blue-600 dark:stroke-blue-400"
      strokeWidth="2.2"
      strokeLinecap="round"
    />
  </svg>
);
