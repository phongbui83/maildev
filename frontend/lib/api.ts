// Thin fetch wrapper around the backend REST API (same origin, cookie session).

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

export async function api<T = unknown>(path: string, init: RequestInit = {}): Promise<T> {
  const headers: Record<string, string> = { ...(init.headers as Record<string, string>) };
  if (init.body && typeof init.body === "string" && !headers["Content-Type"]) {
    headers["Content-Type"] = "application/json";
  }
  const res = await fetch(path, { credentials: "same-origin", ...init, headers });
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  let data: unknown = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = text;
  }
  if (!res.ok) {
    let msg = res.statusText;
    if (data && typeof data === "object" && "error" in data) {
      msg = String((data as { error: unknown }).error);
    }
    throw new ApiError(res.status, msg);
  }
  return data as T;
}

export const post = <T = unknown>(path: string, body?: unknown) =>
  api<T>(path, { method: "POST", body: body === undefined ? undefined : JSON.stringify(body) });
export const patch = <T = unknown>(path: string, body: unknown) =>
  api<T>(path, { method: "PATCH", body: JSON.stringify(body) });
export const del = <T = unknown>(path: string) => api<T>(path, { method: "DELETE" });

// --- types mirrored from the Go model ---

export type Role = "admin" | "auditor" | "user";
export type MemberRole = "owner" | "member";

export interface User {
  id: string;
  ldap_uuid?: string;
  username: string;
  display_name: string;
  email: string;
  department: string;
  role: Role;
  auth_source: "ldap" | "local";
  disabled: boolean;
  must_change_password: boolean;
  locked_until?: string;
  created_at: string;
  last_login_at?: string;
}

export interface Project {
  id: string;
  slug: string;
  name: string;
  description: string;
  status: "active" | "archived";
  auto_join: boolean;
  relay_enabled: boolean;
  retention_days: number;
  max_emails: number;
  created_at: string;
  updated_at: string;
  my_role?: "admin" | "auditor" | MemberRole | "";
  email_count: number;
  unread_count: number;
}

export interface Member {
  project_id: string;
  user_id: string;
  username: string;
  display_name: string;
  email: string;
  role: MemberRole;
  added_at: string;
  pending: boolean;
}

export interface Credential {
  id: string;
  project_id: string;
  kind: "smtp" | "api" | "ses";
  label: string;
  username: string;
  secret?: string;
  created_at: string;
  last_used_at?: string;
}

export interface Address {
  name: string;
  address: string;
}

export interface Attachment {
  id: string;
  email_id: string;
  filename: string;
  content_type: string;
  size: number;
  content_id: string;
  inline: boolean;
}

export interface Email {
  id: string;
  project_id: string;
  uid: number;
  source: string;
  client_ip: string;
  received_at: string;
  message_id: string;
  subject: string;
  from: Address[];
  to: Address[];
  cc: Address[];
  bcc: Address[];
  envelope_from: string;
  envelope_to: string[];
  sent_at?: string;
  size: number;
  text_body?: string;
  html_body?: string;
  headers?: { key: string; value: string }[];
  attachment_count: number;
  smime?: "" | "signed" | "signed-opaque" | "encrypted";
  smime_valid?: boolean;
  smime_signer?: string;
  smime_error?: string;
  seen: boolean;
  flagged: boolean;
  snippet?: string;
  attachments?: Attachment[];
  relay_status: RelayStatus;
  relay_requested_by?: string;
  relay_requested_at?: string;
  relay_decided_by?: string;
  relay_decided_at?: string;
  relay_error?: string;
  relay_remote_id?: string;
  relay_note?: string;
}

export type RelayStatus = "none" | "pending" | "sent" | "failed" | "rejected";

export interface RelayConfig {
  project_id: string;
  kind: "smtp" | "ses" | "sendgrid";
  host: string;
  port: number;
  security: "none" | "starttls" | "tls";
  tls_insecure: boolean;
  username: string;
  has_secret: boolean;
  region: string;
  access_key_id: string;
  endpoint: string;
  from_override: string;
  allowed_recipients: string;
  updated_by?: string;
  updated_at: string;
}

export interface RelayState {
  enabled: boolean;
  config: RelayConfig | null;
}

export interface RelayOutcome {
  id: string;
  status: string;
  error?: string;
  remote_id?: string;
}

export interface AuditEntry {
  id: number;
  at: string;
  actor: string;
  project_id?: string;
  project_slug?: string;
  action: string;
  target: string;
  detail: Record<string, unknown>;
  ip: string;
  protocol: string;
}

export interface ServerConfig {
  public_url: string;
  mail_host: string;
  smtp_ports: string[];
  smtps_ports: string[];
  imap_ports: string[];
  imaps_ports: string[];
  pop3_ports: string[];
  pop3s_ports: string[];
  auth_mode: string;
  edition?: "full" | "lite";
  encryption: boolean;
  directory?: boolean;
  max_message_bytes: number;
}

export interface Paged<T> {
  items: T[];
  total: number;
}

export function canManage(p?: Project | null) {
  return p?.my_role === "admin" || p?.my_role === "owner";
}
export function canWrite(p?: Project | null) {
  if (!p || p.status === "archived") return false; // archived: inbox is read-only
  return p.my_role === "admin" || p.my_role === "owner" || p.my_role === "member";
}
