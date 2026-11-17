import { useQuery } from '@tanstack/react-query';
import { useApi } from '../../../shared/api/client';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '../../../shared/ui/select';
import { packOptions } from '../api/queries';
export function PackField({
  value,
  onChange,
  defaultValue = '',
}: {
  value?: string;
  onChange?: (value: string) => void;
  defaultValue?: string;
}) {
  const packs = useQuery(packOptions(useApi()));
  return (
    <label className="block space-y-2 text-xs font-medium">
      Pack (optional)
      <Select
        name={onChange ? undefined : 'pack_id'}
        value={value === undefined ? undefined : value || 'none'}
        defaultValue={defaultValue || 'none'}
        onValueChange={(id) => onChange?.(id === 'none' ? '' : id)}
        disabled={packs.isPending || packs.isError}
      >
        <SelectTrigger className="w-full" aria-label="Pack">
          <SelectValue placeholder="No pack" />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="none">No pack</SelectItem>
          {packs.data?.map((pack) => (
            <SelectItem key={pack.id} value={pack.id}>
              {pack.name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {packs.isError && (
        <span role="alert" className="text-destructive">
          Could not load packs. Reopen to retry.
        </span>
      )}
    </label>
  );
}
