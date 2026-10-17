export const z = { enum: (vals) => ({ safeParse: (v) => ({ data: vals.includes(v) ? v : undefined }) }) };
