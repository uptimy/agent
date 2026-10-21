import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Megaphone } from "lucide-react";
import { api, type Announcement } from "@/lib/api";
import { formatDateTime, fromLocalInput, timeAgo, toLocalInput } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Field, Input, Switch, Textarea } from "@/components/ui/input";
import { confirm } from "@/components/ui/confirm";
import { toast } from "@/components/ui/toast";
import { ErrorNote } from "@/components/Layout";

/**
 * The status page's one announcement. It's saved on its own, apart from the
 * page's other settings, so posting one never waits on (or publishes)
 * unsaved edits.
 */
export function AnnouncementEditor({ canEdit, onChange }: { canEdit: boolean; onChange: () => void }) {
  const current = useQuery({ queryKey: ["announcement"], queryFn: api.announcement });
  if (!current.isSuccess) return <ErrorNote error={current.error} />;
  // Remount when the saved one changes, so the form starts from it.
  return (
    <AnnouncementForm
      key={current.data?.updated_at ?? "none"}
      current={current.data}
      canEdit={canEdit}
      onChange={onChange}
    />
  );
}

function AnnouncementForm({
  current,
  canEdit,
  onChange,
}: {
  current: Announcement | null;
  canEdit: boolean;
  onChange: () => void;
}) {
  const qc = useQueryClient();
  const expired = !!current?.show_until && new Date(current.show_until) <= new Date();
  const live = !!current && !expired;
  const [title, setTitle] = useState(current?.title ?? "");
  const [message, setMessage] = useState(current?.message ?? "");
  const [until, setUntil] = useState<string | null>(current?.show_until ?? null);
  const dirty =
    title !== (current?.title ?? "") || message !== (current?.message ?? "") || until !== (current?.show_until ?? null);

  const done = (a: Announcement | null, msg: string) => {
    qc.setQueryData(["announcement"], a);
    onChange();
    toast.success(msg);
  };
  const save = useMutation({
    mutationFn: () => api.saveAnnouncement({ title, message, show_until: until }),
    onSuccess: (a) => done(a, live ? "Announcement updated" : "Announcement posted"),
  });
  const remove = useMutation({
    mutationFn: api.removeAnnouncement,
    onSuccess: () => done(null, "Announcement removed"),
  });

  return (
    <Card section>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Megaphone className="size-4 text-primary" /> Announcement
        </CardTitle>
        <CardDescription>
          {live
            ? `On the page since ${formatDateTime(current!.posted_at)}${current!.show_until ? `, until ${formatDateTime(current!.show_until)}` : ""}.`
            : expired
              ? `Ended ${timeAgo(current!.show_until!)}. Post it again or write a new one.`
              : "Optional. One message under the page's status, for news like a migration or a new region. Incidents go under Incidents."}
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form
          className="flex flex-col gap-5"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          <fieldset disabled={!canEdit} className="flex flex-col gap-5">
            <Field label="Title" htmlFor="an-title">
              <Input
                id="an-title"
                required
                maxLength={150}
                value={title}
                placeholder="We're moving to a new data center"
                onChange={(e) => setTitle(e.target.value)}
              />
            </Field>
            <Field label="Message" htmlFor="an-message" hint="Optional.">
              <Textarea
                id="an-message"
                rows={3}
                maxLength={2000}
                value={message}
                placeholder="On Saturday, October 10 we're moving to Frankfurt. Expect a few minutes of downtime around 02:00 UTC."
                onChange={(e) => setMessage(e.target.value)}
              />
            </Field>
            <div className="flex flex-col gap-3">
              <Switch
                id="an-until"
                checked={until != null}
                onChange={(v) => setUntil(v ? new Date(Date.now() + 7 * 24 * 3600_000).toISOString() : null)}
                label="Take it down automatically"
              />
              {until != null && (
                <Field label="Show until" htmlFor="an-until-at">
                  <Input
                    id="an-until-at"
                    type="datetime-local"
                    required
                    className="w-fit"
                    value={toLocalInput(until)}
                    onChange={(e) => e.target.value && setUntil(fromLocalInput(e.target.value))}
                  />
                </Field>
              )}
            </div>
          </fieldset>
          <ErrorNote error={save.error ?? remove.error} />
          {canEdit && (
            <div className="flex justify-end gap-2">
              {current && (
                <Button
                  type="button"
                  variant="ghost"
                  disabled={remove.isPending}
                  onClick={() =>
                    confirm({
                      title: "Remove announcement",
                      message: (
                        <>
                          <strong className="text-foreground">{current.title}</strong> will no longer show on the status
                          page.
                        </>
                      ),
                      confirmLabel: "Remove",
                      action: () => remove.mutateAsync(),
                    })
                  }
                >
                  Remove
                </Button>
              )}
              <Button type="submit" disabled={save.isPending || (live && !dirty)}>
                {live ? "Save changes" : "Post announcement"}
              </Button>
            </div>
          )}
        </form>
      </CardContent>
    </Card>
  );
}
