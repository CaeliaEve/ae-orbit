// Display metadata comes from the live catalog; saved production intent stays intact.
export function draftDisplayItem(entry, itemCache) {
  const saved = entry.item;
  const current = itemCache.get(String(saved.id));
  return current && current.name === saved.name && current.damage === saved.damage
    && (current.kind || "item") === (saved.kind || "item") ? current : saved;
}

export function draftSelectionSummary(entries, unchecked) {
  const editable = entries.filter(entry => entry.status === "draft");
  const selected = editable.filter(entry => !unchecked.has(entry.id));
  return { editable: editable.length, selected: selected.length,
    quantity: selected.reduce((sum, entry) => sum + BigInt(entry.quantity), 0n).toString(),
    ...(selected.some(entry=>entry.item?.kind === "fluid") ? { quantityByKind: selected.reduce((totals,entry)=>{
      const kind=entry.item?.kind || "item";totals[kind]=(BigInt(totals[kind] || "0")+BigInt(entry.quantity)).toString();return totals;
    },{}) } : {}) };
}
