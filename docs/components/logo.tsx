import { cn } from '@/lib/cn';
import { basePath } from '@/lib/shared';

// Official Penhoon UI logo (media/p-ui-{light,dark}.png in the product repo).
// Theme-aware via Tailwind's `dark:` variant. Pass a height class (e.g. `h-6`);
// width scales automatically (the artwork is 2:1).
// Plain <img> tags are left untouched by `basePath`, so prefix it explicitly —
// otherwise these 404 on the GitHub Pages project page.
export function Logo({ className }: { className?: string }) {
  return (
    <>
      {/* eslint-disable-next-line @next/next/no-img-element */}
      <img
        src={`${basePath}/logo-light.png`}
        alt="Penhoon UI"
        className={cn('w-auto dark:hidden', className)}
      />
      {/* eslint-disable-next-line @next/next/no-img-element */}
      <img
        src={`${basePath}/logo-dark.png`}
        alt="Penhoon UI"
        className={cn('hidden w-auto dark:block', className)}
      />
    </>
  );
}
