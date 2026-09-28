const baseUrl = `${(import.meta.env?.BASE_URL ?? "/").replace(/\/+$/, "")}/`;

export function sitePath(relativePath = "", base = baseUrl) {
  const normalizedBase = `${base.replace(/\/+$/, "")}/`;
  const cleanPath = relativePath.replace(/^\/+|\/+$/g, "");
  return cleanPath.length === 0 ? normalizedBase : `${normalizedBase}${cleanPath}/`;
}
