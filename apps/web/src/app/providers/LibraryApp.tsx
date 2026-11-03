import { useState } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ApiContext, createApi } from '../../shared/api/client';
import { LibraryPage } from '../../pages/_library';
import { Toaster } from '../../shared/ui/sonner';

export default function LibraryApp({ apiBase }: { apiBase: string }) {
  const [queryClient] = useState(
    () => new QueryClient({ defaultOptions: { queries: { retry: 1, staleTime: 30_000 } } }),
  );
  const [api] = useState(() => createApi(apiBase));
  return (
    <ApiContext.Provider value={api}>
      <QueryClientProvider client={queryClient}>
        <LibraryPage />
        <Toaster />
      </QueryClientProvider>
    </ApiContext.Provider>
  );
}
