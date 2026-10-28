/** Where "contact Zypher Systems" sends people. */
export const SUPPORT_EMAIL = "support@zyphersystems.com";

/** Prefilled bug and feature reports. The instance ID is not included. */
export const GITHUB_ISSUES_NEW = "https://github.com/zypher-systems/Argus/issues/new";

/**
 * A mail link to support that already says which Argus is asking, since
 * that is the first thing support needs to know.
 */
export function supportMailto(subject: string, version?: string, instanceId?: string): string {
  const lines = ["", "", "--", version ? `Argus ${version}` : "Argus"];
  if (instanceId) lines.push(`Instance ${instanceId}`);
  return `mailto:${SUPPORT_EMAIL}?subject=${encodeURIComponent(subject)}&body=${encodeURIComponent(lines.join("\n"))}`;
}
