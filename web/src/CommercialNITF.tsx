import { commercialNITF, commercialSummary } from "./commercialNITF";

export function CommercialNITF({ metadata, acquiredAt }: { metadata: Record<string, unknown>; acquiredAt?: string | null }) {
  const c = commercialNITF(metadata);
  return <section className="commercial-nitf" aria-label="Commercial NITF">
    <h3>Commercial NITF</h3>
    {!c ? <p>Enrichment unavailable for this record.</p> : <>
      <p>File profile: <strong>{c.profile.status === "unknown" ? "Profile unknown" : `NCDRD — ${c.profile.status}`}</strong> · Conformance not evaluated</p>
      {c.role === "cloud_grid" && <p>Reported role: cloud grid</p>}
      <dl className="metadata-grid">
        <div><dt>Acquisition</dt><dd>{acquiredAt ?? "Unknown"}</dd></div>
        {commercialSummary(c).map((v, i) => <div key={i}>
          <dt>{v.name.replaceAll("_", " ")}</dt>
          <dd title={v.warning || v.source_path}>{String(v.value)}{v.units === "m" ? " m" : ""}
            <small> · {v.semantic_scope.replaceAll("_", " ")}</small></dd>
        </div>)}
        <div><dt>Cloud cover</dt><dd>{c.cloud.value === null ? "Unknown" : `${c.cloud.value}%`}
          {c.cloud.selected_source && <small> · {c.cloud.selected_source}</small>}</dd></div>
      </dl>
      {(c.diagnostics.length > 0 || c.cloud.conflicting) && <details>
        <summary>Warnings ({c.diagnostics.length}){c.cloud.conflicting ? " · Cloud sources disagree" : ""}</summary>
        <ul>{c.diagnostics.map((d, i) => <li key={i}>{d.message} <small>({d.code})</small></li>)}</ul>
      </details>}
      <details><summary>Evidence, sources, and reported metadata</summary><pre>{JSON.stringify(c, null, 2)}</pre></details>
    </>}
  </section>;
}
