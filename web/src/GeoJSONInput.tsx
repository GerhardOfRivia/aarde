import { useState } from "react";
import {
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogContentText,
  DialogTitle,
  TextField,
} from "@mui/material";
import { parseSearchArea } from "./geojson";
import type { Area } from "./types";

export function GeoJSONInput({ onLoad }: { onLoad(area: Area): void }) {
  const [open, setOpen] = useState(false);
  const [text, setText] = useState("");
  const [error, setError] = useState("");

  return (
    <>
      <Button size="small" variant="outlined" onClick={() => setOpen(true)}>
        Paste GeoJSON
      </Button>
      <Dialog open={open} onClose={() => setOpen(false)} fullWidth maxWidth="sm"
        aria-labelledby="geojson-title" aria-describedby="geojson-description">
        <form onSubmit={(event) => {
          event.preventDefault();
          try {
            onLoad(parseSearchArea(text));
            setError("");
            setOpen(false);
          } catch (reason) {
            setError(reason instanceof Error ? reason.message : "Could not load the search area.");
          }
        }}>
          <DialogTitle id="geojson-title">Load a search area</DialogTitle>
          <DialogContent>
            <DialogContentText id="geojson-description" sx={{ mb: 2 }}>
              Paste a Polygon or MultiPolygon in longitude/latitude coordinates.
              Features and FeatureCollections containing only polygons are also supported.
              Loading replaces the current search area; then choose Search catalog.
            </DialogContentText>
            <TextField
              id="geojson-input"
              label="GeoJSON"
              placeholder={'{"type":"Polygon","coordinates":[[[-106,39],[-105,39],[-105,40],[-106,39]]]}'}
              autoFocus
              fullWidth
              multiline
              minRows={8}
              maxRows={16}
              value={text}
              onChange={(event) => { setText(event.target.value); setError(""); }}
              error={Boolean(error)}
              helperText={error || "WGS84 (EPSG:4326). The first and last position of each ring must match."}
              slotProps={{
                htmlInput: { spellCheck: false },
                input: { sx: { fontFamily: "monospace", fontSize: 13 } },
                formHelperText: { role: error ? "alert" : undefined },
              }}
            />
          </DialogContent>
          <DialogActions>
            <Button onClick={() => setOpen(false)}>Cancel</Button>
            <Button type="submit" variant="contained" disabled={!text.trim()}>
              Load Area
            </Button>
          </DialogActions>
        </form>
      </Dialog>
    </>
  );
}
