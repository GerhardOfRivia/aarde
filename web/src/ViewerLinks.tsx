import { Button } from '@mui/material';
import { viewerURL } from './viewer';
export function ViewerLinks({ catalog, image }: { catalog: string; image: string }) {
  const href = viewerURL(catalog, image);
  return <div className="viewer-links">
    <Button size="small" href={href}>Open image viewer</Button>
    <Button size="small" href={href} target="_blank" rel="noopener" aria-label={`Open image viewer for ${image} in a new tab`} title="Open in a new tab">↗</Button>
  </div>;
}
