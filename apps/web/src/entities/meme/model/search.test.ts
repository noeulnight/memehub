import { describe, expect, it } from 'vite-plus/test';
import { parseMemeSearch, formatTagToken, withSafetyFilter } from './search';

describe('uploader search', () => {
  it('separates an uploader from optional free text', () => {
    expect(parseMemeSearch(' @noeulnight  cat mood ')).toEqual({
      uploader: 'noeulnight',
      tags: [],
      text: 'cat mood',
    });
    expect(parseMemeSearch('@NoeulNight')).toEqual({ uploader: 'NoeulNight', tags: [], text: '' });
    expect(parseMemeSearch('@user@example.com cats')).toEqual({
      uploader: 'user@example.com',
      tags: [],
      text: 'cats',
    });
  });
  it('keeps an incomplete handle and inline mentions as text', () => {
    expect(parseMemeSearch('@')).toEqual({ uploader: '', tags: [], text: '@' });
    expect(parseMemeSearch('hello @user')).toEqual({ uploader: '', tags: [], text: 'hello @user' });
  });
  it('combines an uploader, exact tags, and free text', () => {
    expect(parseMemeSearch('@noeulnight #work #"Exact tag" cat')).toEqual({
      uploader: 'noeulnight',
      tags: ['work', 'Exacttag'],
      text: 'cat',
    });
    expect(parseMemeSearch('#work #work')).toEqual({ uploader: '', tags: ['work'], text: '' });
  });
  it('round-trips tags containing spaces and quotes and keeps incomplete tokens', () => {
    for (const tag of ['Exact tag', 'a "quote"', 'path\\tag'])
      expect(parseMemeSearch(formatTagToken(tag)).tags).toEqual([
        tag.normalize('NFC').replace(/\s/gu, ''),
      ]);
    expect(parseMemeSearch('#"incomplete tag').tags).toEqual([]);
    expect(parseMemeSearch('#').text).toBe('#');
  });
});

it('removes tag whitespace while preserving ordinary search text', () => {
  expect(formatTagToken(' 감정\t 표현\u00a0')).toBe('#감정표현');
  expect(parseMemeSearch('#"감정  표현" #감정표현 오늘 기분')).toEqual({
    uploader: '',
    tags: ['감정표현'],
    text: '오늘 기분',
  });
});

it('prioritizes reserved safety tokens over tags and uses the last token', () => {
  expect(parseMemeSearch('@noeul #work #NSFW cat')).toEqual({
    uploader: 'noeul',
    tags: ['work'],
    text: 'cat',
    safetyRating: 'nsfw',
  });
  expect(parseMemeSearch('#nsfw sfw').safetyRating).toBe('sfw');
  expect(parseMemeSearch('sfw #nsfw').safetyRating).toBe('nsfw');
  expect(parseMemeSearch('#"SFW"').tags).toEqual([]);
  expect(parseMemeSearch('#sfwcat nsfwish').text).toBe('nsfwish');
  expect(parseMemeSearch('#sfwcat').tags).toEqual(['sfwcat']);
});

it('switches safety filters without losing uploader, tags or free text', () => {
  expect(withSafetyFilter('@noeul #work cat #nsfw', 'sfw')).toBe('@noeul #work cat #sfw');
  expect(withSafetyFilter('#sfw cat', '')).toBe('cat');
});
