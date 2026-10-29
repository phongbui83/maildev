// MailDev mark: envelope on a blue tile with dev brackets and a
// green "captured" dot. Same artwork as app/icon.svg (favicon).
export default function Logo({ size = 22 }: { size?: number }) {
  return (
    <svg viewBox="0 0 64 64" width={size} height={size} aria-hidden="true">
      <defs>
        <linearGradient id="lmdg" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0" stopColor="#1b7fd6" />
          <stop offset="1" stopColor="#0b4f8f" />
        </linearGradient>
      </defs>
      <rect x="2" y="2" width="60" height="60" rx="14" fill="url(#lmdg)" />
      <rect x="12" y="19" width="40" height="28" rx="5" fill="#fff" />
      <path d="M14 23 L32 37 L50 23" fill="none" stroke="#0b4f8f" strokeWidth="3.2" strokeLinecap="round" strokeLinejoin="round" />
      <path d="M22 44 L17 50 L22 56 M42 44 L47 50 L42 56" fill="none" stroke="#9be7a4" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round" />
      <circle cx="50" cy="17" r="6" fill="#34c759" stroke="#fff" strokeWidth="2" />
    </svg>
  );
}
