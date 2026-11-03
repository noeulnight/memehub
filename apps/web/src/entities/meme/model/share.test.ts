import { describe, expect, it } from 'vite-plus/test';
import { memeShareURLs } from './share';
describe('share URLs', () => {
  it.each([
    ['image/gif', 'gif'],
    ['image/png', 'png'],
    ['image/jpeg', 'jpg'],
    ['image/webp', 'webp'],
  ])('uses the actual extension for %s', (mime, extension) => {
    expect(memeShareURLs('id', 'https://memes.example', '/api', mime).media).toBe(
      `https://memes.example/api/memes/id/file.${extension}`,
    );
  });
  it('creates stable links without leaking current filters or signed query parameters', () => {
    expect(memeShareURLs('meme-id', 'https://memes.example/?q=cats#top', '/api/')).toEqual({
      detail: 'https://memes.example/?meme=meme-id',
      media: 'https://memes.example/api/memes/meme-id/file',
    });
  });
  it('uses the public API origin when the API is hosted separately', () => {
    expect(memeShareURLs('meme-id', 'https://memes.example', 'https://api.example/api')).toEqual({
      detail: 'https://memes.example/?meme=meme-id',
      media: 'https://api.example/api/memes/meme-id/file',
    });
  });
  it('encodes the meme identifier instead of interpreting it as a URL path', () => {
    const urls = memeShareURLs('id?query#fragment', 'https://memes.example', '/api');
    expect(new URL(urls.detail).searchParams.get('meme')).toBe('id?query#fragment');
    expect(urls.media).toBe('https://memes.example/api/memes/id%3Fquery%23fragment/file');
  });
});
