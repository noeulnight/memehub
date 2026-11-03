import { Button } from '../../../shared/ui/button';
import { BookmarkIcon } from '../../../shared/ui/icons';

export function MemeSave({ onSave }: { onSave: () => void }) {
  return (
    <Button
      type="button"
      variant="secondary"
      size="icon-sm"
      aria-label="Add to collection"
      title="Add to collection"
      onClick={onSave}
    >
      <BookmarkIcon aria-hidden="true" />
    </Button>
  );
}
