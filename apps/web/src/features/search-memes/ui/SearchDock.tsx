import { useEffect, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useApi } from '../../../shared/api/client';
import {
  Command,
  CommandInput,
  CommandList,
  CommandItem,
  CommandEmpty,
} from '../../../shared/ui/command';
import { UserIcon } from '../../../shared/ui/icons';
import { reservedSafety } from '../../../entities/meme';
import { getSuggestions, type Suggestion } from '../api/suggestions';
import { searchCompletion, applyCompletion } from '../model/completion';

export function SearchDock({
  query,
  onSearch,
}: {
  query: string;
  onSearch: (value: string) => void;
}) {
  const api = useApi();
  const [draft, setDraft] = useState(query);
  const [composing, setComposing] = useState(false);
  const [focused, setFocused] = useState(false);
  const [caret, setCaret] = useState(query.length);
  const [dismissed, setDismissed] = useState<string | null>(null);
  const [delayed, setDelayed] = useState<{ kind: 'user' | 'tag'; query: string } | null>(null);
  const onSearchRef = useRef(onSearch);
  const searchInput = useRef<HTMLInputElement>(null);
  useEffect(() => {
    onSearchRef.current = onSearch;
  }, [onSearch]);
  useEffect(
    () => setDraft((current) => (current.trim() === query.trim() ? current : query)),
    [query],
  );
  useEffect(() => {
    if (composing || draft.trim() === query.trim()) return;
    const timer = window.setTimeout(() => onSearchRef.current(draft), 300);
    return () => window.clearTimeout(timer);
  }, [draft, query, composing]);
  const token = searchCompletion(draft, caret);
  const open = focused && !composing && token !== null && dismissed !== draft.trim();
  const kind = token?.kind;
  const prefix = token?.query;
  useEffect(() => {
    if (!open || !kind || prefix === undefined) {
      setDelayed(null);
      return;
    }
    const timer = window.setTimeout(() => setDelayed({ kind, query: prefix }), 200);
    return () => window.clearTimeout(timer);
  }, [open, kind, prefix]);
  const ready = open && delayed?.kind === kind && delayed?.query === prefix;
  const suggestions = useQuery({
    queryKey: ['search-suggestions', delayed?.kind, delayed?.query],
    queryFn: ({ signal }) => getSuggestions(api, delayed!.kind, delayed!.query, signal),
    enabled: ready,
  });
  useEffect(() => {
    const shortcut = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault();
        searchInput.current?.focus();
      }
    };
    window.addEventListener('keydown', shortcut);
    return () => window.removeEventListener('keydown', shortcut);
  }, []);
  function choose(item: Suggestion) {
    if (!token) return;
    const next = applyCompletion(draft, token, item.value);
    setDraft(next.value);
    setCaret(next.caret);
    setDismissed(next.value.trim());
    onSearchRef.current(next.value);
    requestAnimationFrame(() => {
      searchInput.current?.focus();
      searchInput.current?.setSelectionRange(next.caret, next.caret);
    });
  }
  const reserved =
    kind === 'tag'
      ? ['sfw', 'nsfw']
          .filter((value) => value.startsWith((prefix || '').toLowerCase()))
          .map((value) => ({ value, label: `${value.toUpperCase()} filter` }))
      : [];
  const candidates = [
    ...reserved,
    ...(ready ? suggestions.data || [] : []).filter(
      (item) => kind !== 'tag' || !reservedSafety(item.value),
    ),
  ];
  return (
    <form
      role="search"
      onSubmit={(event) => {
        event.preventDefault();
        if (!composing) {
          onSearch(draft);
          setDismissed(draft.trim());
        }
      }}
      className="relative min-w-0 flex-1 rounded-full border border-border bg-muted/95 px-4 py-1.5 backdrop-blur-xl"
    >
      <Command
        label="Search memes"
        shouldFilter={false}
        loop
        className="overflow-visible rounded-none bg-transparent [&_[data-slot=command-input-wrapper]]:h-8 [&_[data-slot=command-input-wrapper]]:border-0 [&_[data-slot=command-input-wrapper]]:px-0 [&_[data-slot=command-input-wrapper]>svg]:hidden"
      >
        <CommandInput
          ref={searchInput}
          value={draft}
          maxLength={200}
          onValueChange={(value) => {
            setDraft(value);
            setCaret(searchInput.current?.selectionStart ?? value.length);
          }}
          onSelect={() => setCaret(searchInput.current?.selectionStart ?? draft.length)}
          onFocus={() => setFocused(true)}
          onBlur={() => setFocused(false)}
          onCompositionStart={() => setComposing(true)}
          onCompositionEnd={(event) => {
            setDraft(event.currentTarget.value);
            setCaret(event.currentTarget.selectionStart ?? event.currentTarget.value.length);
            setComposing(false);
          }}
          onKeyDown={(event) => {
            if (event.nativeEvent.isComposing || composing) return;
            if (event.key === 'Escape') {
              event.preventDefault();
              event.stopPropagation();
              setDismissed(draft.trim());
            }
            if (event.key === 'ArrowDown' || event.key === 'ArrowUp') setDismissed(null);
          }}
          aria-expanded={open}
          placeholder="Search, @user, #tag, #sfw"
          autoComplete="off"
          className="h-8 rounded-none px-0 py-0 shadow-none focus-visible:ring-0"
        />
        {open && (
          <CommandList className="absolute right-0 bottom-full left-0 mb-3 max-h-64 rounded-lg border border-border bg-popover p-1 text-popover-foreground shadow-md">
            <CommandEmpty>
              {!ready || suggestions.isPending
                ? 'Searching…'
                : suggestions.isError
                  ? 'Suggestions unavailable.'
                  : 'No matches.'}
            </CommandEmpty>
            {candidates.map((item) => (
              <CommandItem
                key={item.value}
                value={item.value}
                onMouseDown={(event) => event.preventDefault()}
                onSelect={() => choose(item)}
              >
                {kind === 'user' && <UserIcon aria-hidden="true" />}
                <span className="min-w-0 flex-1 truncate">
                  {kind === 'user' ? `@${item.value}` : item.value}
                </span>
                {item.label !== item.value && (
                  <span className="max-w-28 truncate text-xs text-muted-foreground">
                    {item.label}
                  </span>
                )}
              </CommandItem>
            ))}
          </CommandList>
        )}
      </Command>
    </form>
  );
}
