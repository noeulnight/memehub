import type { Meme } from '../../../entities/meme';

export function groupMemesByPack(items: Meme[]) {
  const groups = new Map<string, { id: string; name: string; items: Meme[] }>();
  for (const meme of items) {
    const id = meme.pack_id || '';
    let group = groups.get(id);
    if (!group) {
      group = { id, name: id ? meme.pack_name || 'Unnamed pack' : 'Other', items: [] };
      groups.set(id, group);
    }
    group.items.push(meme);
  }
  const other = groups.get('');
  groups.delete('');
  return [...groups.values(), ...(other ? [other] : [])];
}
