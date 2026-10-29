// Inline SVG flags (emoji flags do not render on Windows).
export function FlagVN({ size = 16 }: { size?: number }) {
  return (
    <svg width={size} height={size * 0.7} viewBox="0 0 30 20" aria-hidden="true">
      <rect width="30" height="20" fill="#da251d" />
      <polygon fill="#ff0" points="15,4 16.76,9.05 22.13,9.05 17.8,12.2 19.45,17.3 15,14.2 10.55,17.3 12.2,12.2 7.87,9.05 13.24,9.05" />
    </svg>
  );
}

export function FlagGB({ size = 16 }: { size?: number }) {
  return (
    <svg width={size} height={size * 0.7} viewBox="0 0 60 30" aria-hidden="true">
      <clipPath id="gbclip"><rect width="60" height="30" /></clipPath>
      <g clipPath="url(#gbclip)">
        <rect width="60" height="30" fill="#012169" />
        <path d="M0,0 L60,30 M60,0 L0,30" stroke="#fff" strokeWidth="6" />
        <path d="M0,0 L60,30 M60,0 L0,30" stroke="#C8102E" strokeWidth="2" />
        <path d="M30,0 V30 M0,15 H60" stroke="#fff" strokeWidth="10" />
        <path d="M30,0 V30 M0,15 H60" stroke="#C8102E" strokeWidth="6" />
      </g>
    </svg>
  );
}

export function FlagJP({ size = 16 }: { size?: number }) {
  return (
    <svg width={size} height={size * 0.7} viewBox="0 0 30 20" aria-hidden="true">
      <rect width="30" height="20" fill="#fff" stroke="#ddd" strokeWidth="0.5" />
      <circle cx="15" cy="10" r="6" fill="#bc002d" />
    </svg>
  );
}

export function Flag({ code, size }: { code: string; size?: number }) {
  if (code === "vi") return <FlagVN size={size} />;
  if (code === "ja") return <FlagJP size={size} />;
  return <FlagGB size={size} />;
}
