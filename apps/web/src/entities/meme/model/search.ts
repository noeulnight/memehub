export type SafetyFilter = '' | 'sfw' | 'nsfw';

export function reservedSafety(value: string): SafetyFilter {
  const normalized = value.toLowerCase();
  return normalized === 'sfw' || normalized === 'nsfw' ? normalized : '';
}

export function formatTagToken(tag: string) {
  tag = tag.replace(/\s/gu, '').normalize('NFC');
  return `#${/[\s"\\]/u.test(tag) ? JSON.stringify(tag) : tag}`;
}

export function parseMemeSearch(value: string) {
  const trimmed = value.trim();
  const match = /^@(\S+)(?:\s+|$)/u.exec(trimmed);
  const uploader = match?.[1] || '';
  const body = match ? trimmed.slice(match[0].length) : trimmed;
  const tags: string[] = [];
  let safetyRating: SafetyFilter = '';
  const text = body
    .replace(
      /(^|\s)(?:#("(?:\\.|[^"\\])*"|\S+)|(sfw|nsfw)(?=\s|$))/giu,
      (whole, space: string, token: string | undefined, bare: string | undefined) => {
        if (bare) {
          safetyRating = reservedSafety(bare);
          return space;
        }
        if (!token) return whole;
        let tag = token;
        if (token.startsWith('"')) {
          try {
            tag = JSON.parse(token);
          } catch {
            return whole;
          }
        }
        tag = tag.replace(/\s/gu, '').normalize('NFC');
        if (!tag) return whole;
        const reserved = reservedSafety(tag);
        if (reserved) safetyRating = reserved;
        else tags.push(tag);
        return space;
      },
    )
    .trim();
  return { uploader, tags: [...new Set(tags)], text, ...(safetyRating ? { safetyRating } : {}) };
}

export function withSafetyFilter(value: string, rating: SafetyFilter) {
  const search = parseMemeSearch(value);
  return [
    search.uploader && `@${search.uploader}`,
    ...search.tags.map(formatTagToken),
    search.text,
    rating && `#${rating}`,
  ]
    .filter(Boolean)
    .join(' ');
}
