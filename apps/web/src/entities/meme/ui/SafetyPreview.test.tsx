// @vitest-environment happy-dom
import { act, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { expect, it } from 'vite-plus/test';
import { SafetyPreview } from './SafetyPreview';

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
it('blurs and disables hidden actions until explicitly revealed', async () => {
  const host = document.createElement('div');
  document.body.append(host);
  const root = createRoot(host);
  function Preview() {
    const [hidden, setHidden] = useState(true);
    return (
      <SafetyPreview hidden={hidden} onReveal={() => setHidden(false)}>
        <button type="button">Copy image</button>
      </SafetyPreview>
    );
  }
  try {
    await act(async () => root.render(<Preview />));
    const hidden = host.querySelector('[inert]');
    expect(hidden?.getAttribute('aria-hidden')).toBe('true');
    expect(hidden?.classList.contains('blur-xl')).toBe(true);
    const reveal = [...host.querySelectorAll('button')].find(
      (button) => button.textContent === 'Show NSFW',
    )!;
    expect(reveal.closest('[inert]')).toBeNull();
    await act(async () => reveal.click());
    expect(host.querySelector('[inert]')).toBeNull();
    expect(host.textContent).toBe('Copy image');
  } finally {
    await act(async () => root.unmount());
    host.remove();
  }
});
