import { endpoint } from '../../../shared/api/client';
import type { Meme } from './types';
import { memeShareURLs } from './share';
export const siteTitle = 'Memehub — The internet, collected.';
export const siteDescription =
  'A home for image and text memes. Search the collection and find your next perfect reaction.';
function concise(value: string, length: number) {
  const text = value.replace(/\s+/gu, ' ').trim();
  return Array.from(text).length > length
    ? Array.from(text)
        .slice(0, length - 1)
        .join('') + '…'
    : text;
}
export function metadata(appBase: string, apiBase: string, meme?: Meme) {
  const home = new URL(appBase);
  home.search = '';
  home.hash = '';
  const urls = meme ? memeShareURLs(meme.id, home.href, apiBase, meme.mime_type) : null;
  let imageWidth = meme?.content_type === 'image' ? meme.width : 1200;
  let imageHeight = meme?.content_type === 'image' ? meme.height : 630;
  if (
    imageWidth &&
    imageHeight &&
    Math.max(imageWidth, imageHeight) > 640 &&
    meme?.content_type === 'image'
  ) {
    const originalWidth = imageWidth;
    const originalHeight = imageHeight;
    if (originalWidth >= originalHeight) {
      imageWidth = 640;
      imageHeight = Math.max(1, Math.floor((originalHeight * 640) / originalWidth));
    } else {
      imageWidth = Math.max(1, Math.floor((originalWidth * 640) / originalHeight));
      imageHeight = 640;
    }
  }
  return {
    title: meme
      ? `${concise(meme.title || meme.body_text || (meme.content_type === 'image' ? 'Image meme' : 'Text meme'), 80)} | Memehub`
      : siteTitle,
    description: meme
      ? concise(
          meme.description ||
            meme.body_text ||
            meme.extracted_text ||
            meme.tags.join(', ') ||
            siteDescription,
          160,
        )
      : siteDescription,
    canonical: urls?.detail || home.href,
    image:
      meme?.content_type === 'image'
        ? new URL(endpoint(apiBase, `memes/${encodeURIComponent(meme.id)}/og.png`), home).href
        : new URL('/og.png', home).href,
    twitterCard:
      meme?.content_type === 'image' &&
      (!meme.width || !meme.height || meme.width < 300 || meme.height < 157)
        ? 'summary'
        : 'summary_large_image',
    imageAlt: meme ? meme.title || meme.description || 'Saved meme' : 'Memehub',
    imageWidth,
    imageHeight,
  };
}
