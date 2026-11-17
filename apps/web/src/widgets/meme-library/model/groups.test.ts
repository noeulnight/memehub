import { expect, it } from 'vite-plus/test';
import type { Meme } from '../../../entities/meme';
import { groupMemesByPack } from './groups';

it('groups by explicit pack ID across pages and puts unassigned memes last', () => {
  const meme = (id: string, pack_id: string | null, pack_name: string | null) =>
    ({ id, pack_id, pack_name }) as Meme;
  const items = [
    meme('other', null, null),
    meme('a1', 'a', 'Same name'),
    meme('b1', 'b', 'Same name'),
    meme('a2', 'a', 'Same name'),
  ];
  expect(
    groupMemesByPack(items).map((group) => ({
      id: group.id,
      name: group.name,
      ids: group.items.map((item) => item.id),
    })),
  ).toEqual([
    { id: 'a', name: 'Same name', ids: ['a1', 'a2'] },
    { id: 'b', name: 'Same name', ids: ['b1'] },
    { id: '', name: 'Other', ids: ['other'] },
  ]);
  expect(groupMemesByPack([])).toEqual([]);
});
