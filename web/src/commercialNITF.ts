export interface CommercialValue {
  name: string;
  value: string | number | boolean | null;
  units: string;
  semantic_scope: string;
  source_path: string;
  warning?: string;
}

export interface CommercialNITF {
  version: 1;
  role: string;
  profile: {
    candidate: string;
    status: "likely" | "possible" | "unknown";
    conformance: "not_evaluated";
  };
  values: CommercialValue[];
  cloud: { value: number | null; selected_source: string; conflicting: boolean };
  diagnostics: { code: string; message: string }[];
}

// Read stored annotations only. Missing/unsupported versions are unavailable.
export function commercialNITF(metadata: Record<string, unknown>): CommercialNITF | null {
  const aarde = metadata?._aarde;
  if (!aarde || typeof aarde !== "object") return null;
  const value = (aarde as Record<string, unknown>).commercial_nitf;
  if (!value || typeof value !== "object") return null;
  const c = value as CommercialNITF;
  if (c.version !== 1 || !c.profile || !["likely", "possible", "unknown"].includes(c.profile.status) ||
    !Array.isArray(c.values) || !Array.isArray(c.diagnostics) || !c.cloud) return null;
  return c;
}

export function commercialSummary(c: CommercialNITF): CommercialValue[] {
  return c.values.filter(v => v.value !== null && (
    ["dataset_platform_code", "dataset_vehicle_id", "dataset_sensor_id", "dataset_product_id",
      "dataset_collection_time", "dataset_processing_time", "sensor_designation", "sensor_name", "sensor_mode"].includes(v.name) ||
    v.units === "m" && v.name.includes("gsd")));
}
