import { CircularProgress, LinearProgress } from '@mui/material';

export function ViewerProgress({ label, detail, value }: { label: string; detail?: string; value?: number }) {
  return <div className="viewer-loading" role="status" aria-atomic="true">
    <div className="viewer-progress">
      <div className="viewer-progress-heading"><CircularProgress size={22} aria-hidden="true" /><span>{label}</span></div>
      {detail && <p>{detail}</p>}
      {value !== undefined && <LinearProgress variant="determinate" value={value} aria-label="Image layer loading progress" aria-valuetext={detail} />}
    </div>
  </div>;
}
