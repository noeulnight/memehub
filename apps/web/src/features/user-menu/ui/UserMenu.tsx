import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { getSession, logoutSession } from '../../../entities/session';
import { apiError, endpoint, useApi } from '../../../shared/api/client';
import { Button } from '../../../shared/ui/button';
import {
  LoginIcon,
  LogoutIcon,
  UserIcon,
  SettingsIcon,
  CollectionIcon,
} from '../../../shared/ui/icons';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from '../../../shared/ui/dropdown-menu';

export function UserMenu({
  mine,
  onMineChange,
  onSettings,
  onCollections,
  inCollection,
}: {
  mine: boolean;
  onSettings: () => void;
  onCollections: () => void;
  inCollection: boolean;
  onMineChange: (mine: boolean) => void;
}) {
  const api = useApi();
  const cache = useQueryClient();
  const session = useQuery({ queryKey: ['session'], queryFn: () => getSession(api) });
  const logout = useMutation({
    mutationFn: () => logoutSession(api),
    onSuccess: async () => {
      await cache.cancelQueries({
        predicate: (query) =>
          query.queryKey[0] === 'collections' ||
          (query.queryKey[0] === 'memes' && !!query.queryKey[5]),
      });
      cache.removeQueries({
        predicate: (query) =>
          query.queryKey[0] === 'collections' ||
          (query.queryKey[0] === 'memes' && !!query.queryKey[5]),
      });
      cache.setQueryData(['session'], null);
      onMineChange(false);
    },
  });
  function login() {
    const base = import.meta.env.PUBLIC_APP_BASE_URL || window.location.origin;
    window.location.assign(
      new URL(endpoint(api.defaults.baseURL || '/api', 'auth/login'), base).href,
    );
  }
  return (
    <div className="relative shrink-0">
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            variant="outline"
            size="icon"
            className="size-[46px] rounded-full bg-muted/95 backdrop-blur-xl dark:bg-muted/95"
            aria-label="Account menu"
            disabled={session.isPending || logout.isPending}
          >
            <UserIcon aria-hidden="true" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent side="top" align="end" sideOffset={8}>
          {session.isError ? (
            <>
              <DropdownMenuLabel>Unable to load account</DropdownMenuLabel>
              <DropdownMenuItem
                onSelect={() => {
                  void session.refetch();
                }}
              >
                Try again
              </DropdownMenuItem>
            </>
          ) : session.data ? (
            <>
              <DropdownMenuLabel>{session.data.display_name || 'Signed in'}</DropdownMenuLabel>
              {session.data.username && (
                <DropdownMenuLabel className="text-xs font-normal text-muted-foreground">
                  @{session.data.username}
                </DropdownMenuLabel>
              )}
              <DropdownMenuItem
                onSelect={() => onMineChange(false)}
                aria-current={!mine && !inCollection ? 'true' : undefined}
              >
                All memes
              </DropdownMenuItem>
              <DropdownMenuItem
                onSelect={() => onMineChange(true)}
                aria-current={mine ? 'true' : undefined}
              >
                My memes
              </DropdownMenuItem>
              <DropdownMenuItem
                onSelect={onCollections}
                aria-current={inCollection ? 'true' : undefined}
              >
                <CollectionIcon aria-hidden="true" /> My collections
              </DropdownMenuItem>
              {session.data.is_admin && (
                <DropdownMenuItem onSelect={onSettings}>
                  <SettingsIcon aria-hidden="true" /> Settings
                </DropdownMenuItem>
              )}
              <DropdownMenuItem onSelect={() => logout.mutate()}>
                <LogoutIcon aria-hidden="true" /> Sign out
              </DropdownMenuItem>
            </>
          ) : (
            <DropdownMenuItem onSelect={login}>
              <LoginIcon aria-hidden="true" /> Sign in
            </DropdownMenuItem>
          )}
        </DropdownMenuContent>
      </DropdownMenu>
      {logout.isError && (
        <p
          role="alert"
          className="absolute right-0 bottom-full mb-3 w-64 text-right text-xs text-destructive"
        >
          {apiError(logout.error)}
        </p>
      )}
    </div>
  );
}
