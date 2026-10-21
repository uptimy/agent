import { useState } from "react";
import {
  DndContext,
  KeyboardSensor,
  PointerSensor,
  closestCenter,
  useSensor,
  useSensors,
  type Announcements,
  type DragEndEvent,
  type UniqueIdentifier,
} from "@dnd-kit/core";
import {
  SortableContext,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { GripVertical, LayoutList, Plus, Search, Trash2, X } from "lucide-react";
import type { MonitorKind, StatusPageConfig, StatusPageMonitor, StatusSection } from "@/lib/api";
import { kinds } from "@/lib/monitors";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Dialog } from "@/components/ui/dialog";
import { EditableText } from "@/components/ui/editable-text";
import { confirm } from "@/components/ui/confirm";
import {
  MAX_SECTIONS,
  addMonitors,
  addSection,
  monitorsIn,
  newSectionId,
  notShown,
  removeMonitor,
  removeSection,
  reorderMonitors,
  reorderSections,
  updateMonitor,
  updateSection,
} from "./layout";

// Uptimy green, the accent when none is set (brand.500).
export const DEFAULT_ACCENT = "#33a36b";

type Edit = (update: (d: StatusPageConfig) => StatusPageConfig) => void;

/**
 * The status page's sections and the monitors in them, as in the Uptimy
 * app's "Service Groups": the page decides what it shows. Drag to reorder,
 * click a name to rename.
 */
export function SectionsEditor({ draft, onChange: edit }: { draft: StatusPageConfig; onChange: Edit }) {
  const [renaming, setRenaming] = useState<string | null>(null); // a new section starts with its name editable
  const [addingTo, setAddingTo] = useState<StatusSection | null>(null);
  const sensors = useDragSensors();
  const available = notShown(draft);

  const newSection = () => {
    const id = newSectionId();
    edit((d) => addSection(d, undefined, id));
    setRenaming(id);
  };
  const onDragEnd = ({ active, over }: DragEndEvent) => {
    if (over) edit((d) => reorderSections(d, String(active.id), String(over.id)));
  };

  return (
    <Card section>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <LayoutList className="size-4" /> Sections
        </CardTitle>
        <CardDescription>
          Group the monitors visitors see. Drag to reorder, and click a name to rename it; internal names stay private.
          Empty sections are hidden.
        </CardDescription>
      </CardHeader>
      {/* The groups are the content: each is a heading line and one framed list. */}
      <CardContent className="flex flex-col gap-8 border-0 bg-transparent p-0">
        {draft.sections.length === 0 ? (
          <div className="flex flex-col items-center gap-4 rounded-xl border-2 border-dashed bg-primary/[0.03] px-6 py-12 text-center">
            <span className="grid size-14 place-items-center rounded-full bg-primary/10 text-primary">
              <LayoutList className="size-7" />
            </span>
            <div>
              <p className="font-semibold">No sections yet</p>
              <p className="mx-auto mt-1 max-w-xs text-sm text-muted-foreground">
                Create a section to organize the monitors your status page shows.
              </p>
            </div>
            <Button onClick={newSection}>
              <Plus /> Create first section
            </Button>
          </div>
        ) : (
          <>
            <DndContext
              sensors={sensors}
              collisionDetection={closestCenter}
              onDragEnd={onDragEnd}
              accessibility={{
                announcements: announcements((id) => draft.sections.find((s) => s.id === id)?.name ?? "section"),
              }}
            >
              <SortableContext items={draft.sections.map((s) => s.id)} strategy={verticalListSortingStrategy}>
                {draft.sections.map((section) => (
                  <SectionCard
                    key={section.id}
                    draft={draft}
                    section={section}
                    renaming={renaming === section.id}
                    edit={edit}
                    onAdd={() => setAddingTo(section)}
                  />
                ))}
              </SortableContext>
            </DndContext>
            {draft.sections.length < MAX_SECTIONS && (
              <Button variant="outline" className="self-start" onClick={newSection}>
                <Plus /> Add section
              </Button>
            )}
          </>
        )}
      </CardContent>

      {addingTo && (
        <AddMonitorsDialog
          section={addingTo}
          available={available}
          onAdd={(ids) => edit((d) => addMonitors(d, ids, addingTo.id))}
          onClose={() => setAddingTo(null)}
        />
      )}
    </Card>
  );
}

/** What screen readers hear while dragging, by name rather than by ID. */
function announcements(nameOf: (id: UniqueIdentifier) => string): Announcements {
  return {
    onDragStart: ({ active }) => `Picked up ${nameOf(active.id)}.`,
    onDragOver: ({ active, over }) =>
      over ? `${nameOf(active.id)} is over ${nameOf(over.id)}.` : `${nameOf(active.id)} is not over a place.`,
    onDragEnd: ({ active, over }) =>
      over ? `${nameOf(active.id)} was moved to where ${nameOf(over.id)} was.` : `${nameOf(active.id)} was dropped.`,
    onDragCancel: ({ active }) => `Moving ${nameOf(active.id)} was canceled.`,
  };
}

function useDragSensors() {
  return useSensors(
    // A few pixels of movement before a drag, so clicks still click.
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );
}

/**
 * A draggable item: renders its children with the drag handle to place, and
 * gets the dragging state for its look.
 */
function Sortable({
  id,
  label,
  className,
  children,
}: {
  id: string | number;
  /** The handle's accessible name, e.g. "Drag to reorder API". */
  label: string;
  className: (dragging: boolean) => string;
  children: (handle: React.ReactNode) => React.ReactNode;
}) {
  const { attributes, listeners, setNodeRef, setActivatorNodeRef, transform, transition, isDragging } = useSortable({
    id,
  });
  const handle = (
    <button
      type="button"
      ref={setActivatorNodeRef}
      {...attributes}
      {...listeners}
      aria-label={label}
      className="grid size-7 shrink-0 cursor-grab touch-none place-items-center rounded-md text-muted-foreground hover:bg-nav-hover hover:text-foreground active:cursor-grabbing"
    >
      <GripVertical className="size-4" />
    </button>
  );
  return (
    <div
      ref={setNodeRef}
      style={{ transform: CSS.Transform.toString(transform), transition, zIndex: isDragging ? 10 : undefined }}
      className={className(isDragging)}
    >
      {children(handle)}
    </div>
  );
}

function SectionCard({
  draft,
  section,
  renaming,
  edit,
  onAdd,
}: {
  draft: StatusPageConfig;
  section: StatusSection;
  renaming: boolean;
  edit: Edit;
  onAdd: () => void;
}) {
  const sensors = useDragSensors();
  const members = monitorsIn(draft, section.id);
  const name = section.name || "Untitled section";

  const remove = () => {
    if (members.length === 0) {
      edit((d) => removeSection(d, section.id));
      return;
    }
    confirm({
      title: "Delete section",
      subtitle: "Saved when you save the page",
      message: (
        <>
          <strong className="text-foreground">{name}</strong> and its {members.length} monitor
          {members.length === 1 ? "" : "s"} come off the status page. The monitors themselves aren&apos;t affected.
        </>
      ),
      confirmLabel: "Delete section",
      action: async () => edit((d) => removeSection(d, section.id)),
    });
  };
  const onDragEnd = ({ active, over }: DragEndEvent) => {
    if (over) edit((d) => reorderMonitors(d, Number(active.id), Number(over.id)));
  };

  return (
    <Sortable
      id={section.id}
      label={`Drag to reorder section ${name}`}
      className={(dragging) =>
        cn("rounded-xl transition-shadow", dragging && "bg-background shadow-lg ring-1 ring-primary")
      }
    >
      {(handle) => (
        <>
          <div className="flex items-center gap-2">
            {handle}
            <span
              aria-hidden
              className="h-5 w-1 shrink-0 rounded-full"
              style={{ background: draft.accent_color || DEFAULT_ACCENT }}
            />
            <EditableText
              value={section.name}
              onChange={(v) => edit((d) => updateSection(d, section.id, { name: v }))}
              label="Section name"
              placeholder="Untitled section"
              required
              maxLength={60}
              startEditing={renaming}
              className="flex-1 font-semibold"
            />
            <span className="hidden shrink-0 text-xs text-muted-foreground sm:inline">
              {members.length} monitor{members.length === 1 ? "" : "s"}
            </span>
            <button
              type="button"
              aria-label={`Delete section ${name}`}
              data-tip="Delete section"
              onClick={remove}
              className="grid size-8 shrink-0 place-items-center rounded-md text-muted-foreground hover:bg-down/10 hover:text-down"
            >
              <Trash2 className="size-4" />
            </button>
          </div>

          <div className="mt-3 flex flex-col divide-y overflow-hidden rounded-xl border bg-card">
            <DndContext
              sensors={sensors}
              collisionDetection={closestCenter}
              onDragEnd={onDragEnd}
              accessibility={{
                announcements: announcements((id) => {
                  const m = members.find((x) => x.id === id);
                  return m ? m.label || m.name : "monitor";
                }),
              }}
            >
              <SortableContext items={members.map((m) => m.id)} strategy={verticalListSortingStrategy}>
                {members.map((m) => (
                  <MonitorCard key={m.id} m={m} edit={edit} />
                ))}
              </SortableContext>
            </DndContext>
            <button
              type="button"
              onClick={onAdd}
              className="flex items-center justify-center gap-2 px-4 py-2.5 text-sm text-muted-foreground transition-colors hover:bg-nav-hover hover:text-primary"
            >
              <Plus className="size-4" /> Add monitors
            </button>
          </div>
        </>
      )}
    </Sortable>
  );
}

function MonitorCard({ m, edit }: { m: StatusPageMonitor; edit: Edit }) {
  return (
    <Sortable
      id={m.id}
      label={`Drag to reorder ${m.label || m.name}`}
      className={(dragging) =>
        cn(
          "flex items-center gap-2 bg-card py-2 pr-2 pl-1 transition-colors",
          dragging ? "rounded-lg shadow-md ring-1 ring-primary" : "hover:bg-nav-hover",
        )
      }
    >
      {(handle) => (
        <>
          {handle}
          <div className="min-w-0 flex-1">
            <EditableText
              value={m.label}
              onChange={(v) => edit((d) => updateMonitor(d, m.id, { label: v }))}
              label={`Public name for ${m.name}`}
              placeholder={m.name}
              fallback={m.name}
              maxLength={100}
              className="text-sm font-medium"
            />
            <div className="flex items-center gap-1.5 pl-1 text-xs text-muted-foreground">
              <Badge className="py-0">{m.type_label}</Badge>
              {m.label && <span className="truncate">{m.name}</span>}
            </div>
          </div>
          <button
            type="button"
            aria-label={`Remove ${m.label || m.name} from the page`}
            data-tip="Remove from the page"
            onClick={() => edit((d) => removeMonitor(d, m.id))}
            className="grid size-8 shrink-0 place-items-center rounded-md text-muted-foreground hover:bg-down/10 hover:text-down"
          >
            <X className="size-4" />
          </button>
        </>
      )}
    </Sortable>
  );
}

/** Picks monitors that aren't on the page to add to a section. */
function AddMonitorsDialog({
  section,
  available,
  onAdd,
  onClose,
}: {
  section: StatusSection;
  available: StatusPageMonitor[];
  onAdd: (ids: number[]) => void;
  onClose: () => void;
}) {
  const [picked, setPicked] = useState<number[]>([]);
  const [search, setSearch] = useState("");
  const q = search.trim().toLowerCase();
  const groups = (["healthcheck", "heartbeat"] as MonitorKind[])
    .map((kind) => ({
      kind,
      items: available.filter((m) => m.kind === kind && (!q || m.name.toLowerCase().includes(q))),
    }))
    .filter((g) => g.items.length > 0);
  const toggle = (id: number) => setPicked((p) => (p.includes(id) ? p.filter((i) => i !== id) : [...p, id]));

  return (
    <Dialog
      title="Add monitors"
      subtitle={<>to {section.name ? `“${section.name}”` : "this section"}</>}
      icon={Plus}
      onClose={onClose}
      className="max-w-lg"
    >
      <div className="flex min-h-0 flex-col gap-3 border-y p-6">
        {available.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            Every monitor is already on the page. Create more under Healthchecks or Heartbeats.
          </p>
        ) : (
          <>
            {available.length > 6 && (
              <div className="relative">
                <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
                <Input
                  aria-label="Search monitors"
                  placeholder="Search monitors"
                  value={search}
                  onChange={(e) => setSearch(e.target.value)}
                  className="pl-9"
                />
              </div>
            )}
            <div className="max-h-80 min-h-0 overflow-y-auto rounded-lg border">
              {groups.length === 0 && <p className="p-4 text-sm text-muted-foreground">Nothing matches “{search}”.</p>}
              {groups.map((g) => (
                <div key={g.kind}>
                  <div className="sticky top-0 border-b bg-card px-3 py-1.5 text-xs font-medium text-muted-foreground">
                    {kinds[g.kind].title}
                  </div>
                  {g.items.map((m) => (
                    <label
                      key={m.id}
                      className={cn(
                        "flex cursor-pointer items-center gap-3 px-3 py-2.5 text-sm hover:bg-nav-hover",
                        picked.includes(m.id) && "bg-primary/5",
                      )}
                    >
                      <input
                        type="checkbox"
                        checked={picked.includes(m.id)}
                        onChange={() => toggle(m.id)}
                        className="size-4 accent-primary"
                      />
                      <span className="min-w-0 flex-1 truncate font-medium">{m.name}</span>
                      <Badge className="py-0">{m.type_label}</Badge>
                    </label>
                  ))}
                </div>
              ))}
            </div>
          </>
        )}
      </div>
      <div className="flex gap-3 p-6 pt-4">
        <Button variant="outline" className="flex-1" onClick={onClose}>
          Cancel
        </Button>
        <Button
          className="flex-1"
          disabled={picked.length === 0}
          onClick={() => {
            onAdd(picked);
            onClose();
          }}
        >
          <Plus /> {picked.length > 1 ? `Add ${picked.length} monitors` : "Add monitor"}
        </Button>
      </div>
    </Dialog>
  );
}
