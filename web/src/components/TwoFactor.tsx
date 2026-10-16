import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, CheckCircle2, Copy, Download, KeyRound, Loader2, ShieldCheck } from "lucide-react";
import { renderSVG } from "uqr";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Dialog } from "@/components/ui/dialog";
import { Field, Input, PasswordInput } from "@/components/ui/input";
import { CopyField } from "@/components/ui/copy-button";
import { ErrorNote } from "@/components/Layout";
import { toast } from "@/components/ui/toast";

/** Two-factor sign-in for the signed-in user: set up, recovery codes, turn off. */
export function TwoFactorCard() {
  const qc = useQueryClient();
  const status = useQuery({ queryKey: ["two-factor"], queryFn: api.twoFactor });
  const [dialog, setDialog] = useState<"setup" | "codes" | "off" | null>(null);
  // One new secret per click: setting up again replaces the stored one, so
  // the QR code shown must be the one this call returned.
  const setup = useMutation({ mutationFn: api.setupTwoFactor, onSuccess: () => setDialog("setup") });
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["two-factor"] });
    qc.invalidateQueries({ queryKey: ["auth"] });
  };
  const on = status.data?.enabled ?? false;
  const left = status.data?.recovery_codes_left ?? 0;

  return (
    <Card>
      <CardHeader className="flex-row flex-wrap items-start justify-between gap-4">
        <div>
          <CardTitle className="flex items-center gap-2">
            Two-factor sign-in {on && <Badge className="border-up/40 text-up">On</Badge>}
          </CardTitle>
          <CardDescription>
            {on
              ? `Signing in takes your password and a code from your authenticator app. ${left} recovery code${left === 1 ? "" : "s"} left.`
              : "Ask for a code from an authenticator app (1Password, Google Authenticator, Authy, ...) after your password. API tokens keep working."}
          </CardDescription>
        </div>
        {status.isSuccess &&
          (on ? (
            <div className="flex flex-wrap gap-2">
              <Button variant="outline" size="sm" onClick={() => setDialog("codes")}>
                <KeyRound /> New recovery codes
              </Button>
              <Button variant="outline" size="sm" onClick={() => setDialog("off")}>
                Turn off
              </Button>
            </div>
          ) : (
            <Button size="sm" onClick={() => setup.mutate()} disabled={setup.isPending}>
              <ShieldCheck /> Set up
            </Button>
          ))}
      </CardHeader>
      {left > 0 && left <= 3 && (
        <CardContent>
          <p className="text-sm text-paused">
            Only {left} recovery code{left === 1 ? "" : "s"} left. Make new ones before you run out.
          </p>
        </CardContent>
      )}
      <ErrorNote error={status.error ?? setup.error} />
      {dialog === "setup" && setup.data && (
        <SetupDialog
          secret={setup.data.secret}
          uri={setup.data.uri}
          onClose={() => {
            setDialog(null);
            refresh();
          }}
        />
      )}
      {dialog === "codes" && (
        <PasswordDialog
          title="New recovery codes"
          action="Make new codes"
          note="Your current recovery codes stop working."
          run={(password) => api.newRecoveryCodes(password)}
          onClose={() => {
            setDialog(null);
            refresh();
          }}
        />
      )}
      {dialog === "off" && (
        <PasswordDialog
          title="Turn off two-factor sign-in"
          action="Turn off"
          note="Signing in will take only your password again. Your recovery codes stop working."
          run={async (password) => {
            await api.disableTwoFactor(password);
            toast.success("Two-factor sign-in is off");
            return null;
          }}
          onClose={() => {
            setDialog(null);
            refresh();
          }}
        />
      )}
    </Card>
  );
}

/** Scan, confirm with a code, then save the recovery codes. */
function SetupDialog({ secret, uri, onClose }: { secret: string; uri: string; onClose: () => void }) {
  const [code, setCode] = useState("");
  const [showKey, setShowKey] = useState(false);
  const enable = useMutation({ mutationFn: (c: string) => api.enableTwoFactor(c) });
  const complete = code.length === 6;

  if (enable.data) {
    return (
      <SaveCodesDialog
        subtitle="Step 2 of 2"
        codes={enable.data.recovery_codes}
        intro={
          <div className="flex items-start gap-3 rounded-lg border border-up/30 bg-up/10 p-3 text-sm">
            <CheckCircle2 className="mt-0.5 size-5 shrink-0 text-up" />
            <div>
              <p className="font-medium">Two-factor sign-in is on</p>
              <p className="text-muted-foreground">Your other devices were signed out.</p>
            </div>
          </div>
        }
        onClose={onClose}
      />
    );
  }
  return (
    <Dialog
      title="Set up two-factor sign-in"
      subtitle="Step 1 of 2"
      icon={ShieldCheck}
      onClose={onClose}
      busy={enable.isPending}
    >
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (complete) enable.mutate(code);
        }}
      >
        <div className="flex flex-col gap-6 border-y p-6">
          <section>
            <StepHeading n={1} title="Scan the QR code">
              With 1Password, Google Authenticator, Authy or any authenticator app.
            </StepHeading>
            <div className="mt-4 flex flex-col items-center gap-3">
              <div
                className="size-48 rounded-xl bg-white p-3 shadow-sm ring-1 ring-black/10"
                aria-label="QR code for your authenticator app"
                role="img"
                // uqr renders an SVG of the otpauth:// URI; dark on white, so it scans in dark mode too.
                dangerouslySetInnerHTML={{ __html: renderSVG(uri, { border: 0 }) }}
              />
              {showKey ? (
                <div className="w-full">
                  <CopyField text={secret} label="Copy key">
                    <span className="tracking-wider">{secret.match(/.{1,4}/g)?.join(" ")}</span>
                  </CopyField>
                  <p className="mt-1.5 text-xs text-muted-foreground">Time-based, 6 digits, every 30 seconds.</p>
                </div>
              ) : (
                <button
                  type="button"
                  className="text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
                  onClick={() => setShowKey(true)}
                >
                  Can&apos;t scan it? Enter a key instead
                </button>
              )}
            </div>
          </section>

          <section>
            <StepHeading n={2} title="Enter the code it shows">
              The app shows a new 6-digit code every 30 seconds.
            </StepHeading>
            <Input
              id="totp-code"
              aria-label="6-digit code"
              inputMode="numeric"
              autoComplete="one-time-code"
              autoFocus
              placeholder="000000"
              maxLength={6}
              className="mt-4 h-14 text-center font-mono text-2xl tracking-[0.5em] placeholder:text-muted-foreground/40"
              value={code}
              onChange={(e) => {
                const digits = e.target.value.replace(/\D/g, "").slice(0, 6);
                setCode(digits);
                if (enable.isError) enable.reset();
                // Six digits is a whole code: try it straight away.
                if (digits.length === 6 && !enable.isPending) enable.mutate(digits);
              }}
            />
            <div className="mt-3">
              <ErrorNote error={enable.error} />
            </div>
          </section>
        </div>
        <div className="flex gap-3 p-6 pt-4">
          <Button type="button" variant="outline" className="flex-1" onClick={onClose} disabled={enable.isPending}>
            Cancel
          </Button>
          <Button type="submit" className="flex-1" disabled={!complete || enable.isPending}>
            {enable.isPending && <Loader2 className="animate-spin" />}
            Turn on
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

function StepHeading({ n, title, children }: { n: number; title: string; children: React.ReactNode }) {
  return (
    <div className="flex items-start gap-3">
      <span className="grid size-6 shrink-0 place-items-center rounded-full bg-brand/15 text-xs font-semibold text-brand">
        {n}
      </span>
      <div>
        <h3 className="text-sm font-semibold">{title}</h3>
        <p className="text-sm text-muted-foreground">{children}</p>
      </div>
    </div>
  );
}

/** Shows new recovery codes; Done unlocks once they're saved. */
function SaveCodesDialog({
  subtitle,
  codes,
  intro,
  onClose,
}: {
  subtitle?: string;
  codes: string[];
  intro?: React.ReactNode;
  onClose: () => void;
}) {
  const [saved, setSaved] = useState(false);
  return (
    <Dialog title="Save your recovery codes" subtitle={subtitle} icon={KeyRound} onClose={onClose}>
      <div className="flex flex-col gap-4 border-y p-6">
        {intro}
        <RecoveryCodes codes={codes} />
        <label className="flex cursor-pointer items-center gap-2.5 text-sm">
          <input
            type="checkbox"
            className="size-4 accent-primary"
            checked={saved}
            onChange={(e) => setSaved(e.target.checked)}
          />
          I&apos;ve saved these codes somewhere safe
        </label>
      </div>
      <div className="flex gap-3 p-6 pt-4">
        <Button className="flex-1" onClick={onClose} disabled={!saved}>
          Done
        </Button>
      </div>
    </Dialog>
  );
}

/** Confirms with the password, then runs the action; shows new codes if it returns any. */
function PasswordDialog({
  title,
  action,
  note,
  run,
  onClose,
}: {
  title: string;
  action: string;
  note: string;
  run: (password: string) => Promise<{ recovery_codes: string[] } | null>;
  onClose: () => void;
}) {
  const [password, setPassword] = useState("");
  const mutation = useMutation({
    mutationFn: () => run(password),
    onSuccess: (data) => {
      if (!data) onClose();
    },
  });
  if (mutation.data) {
    return <SaveCodesDialog codes={mutation.data.recovery_codes} onClose={onClose} />;
  }
  return (
    <Dialog title={title} onClose={onClose} busy={mutation.isPending}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          mutation.mutate();
        }}
      >
        <div className="flex flex-col gap-4 border-y p-6">
          <p className="text-sm text-muted-foreground">{note}</p>
          <Field label="Your password" htmlFor="confirm-password">
            <PasswordInput
              id="confirm-password"
              autoComplete="current-password"
              required
              autoFocus
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </Field>
          <ErrorNote error={mutation.error} />
        </div>
        <div className="flex gap-3 p-6 pt-4">
          <Button type="button" variant="outline" className="flex-1" onClick={onClose} disabled={mutation.isPending}>
            Cancel
          </Button>
          <Button type="submit" className="flex-1" disabled={mutation.isPending}>
            {action}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

/** The codes, once: each signs in one time when the phone is gone. */
function RecoveryCodes({ codes }: { codes: string[] }) {
  const text = codes.join("\n");
  const [copied, setCopied] = useState(false);
  const download = () => {
    const url = URL.createObjectURL(new Blob([text + "\n"], { type: "text/plain" }));
    const a = Object.assign(document.createElement("a"), { href: url, download: "uptimy-agent-recovery-codes.txt" });
    a.click();
    URL.revokeObjectURL(url);
  };
  return (
    <div>
      <p className="text-sm text-muted-foreground">
        If you lose your phone, each code signs you in once. Keep them in a password manager. They won&apos;t be shown
        again.
      </p>
      <ol className="mt-3 grid grid-cols-2 gap-x-4 gap-y-2 rounded-lg border bg-muted/40 p-4 font-mono text-sm">
        {codes.map((c, i) => (
          <li key={c} className="flex gap-2">
            <span className="w-5 text-right text-muted-foreground/60 tabular-nums">{i + 1}.</span>
            <span>{c}</span>
          </li>
        ))}
      </ol>
      <div className="mt-3 grid grid-cols-2 gap-2">
        <Button
          variant="outline"
          size="sm"
          onClick={() =>
            navigator.clipboard.writeText(text).then(
              () => {
                setCopied(true);
                window.setTimeout(() => setCopied(false), 1500);
              },
              () => toast.error("Couldn't copy", "Select the codes and copy them instead."),
            )
          }
        >
          {copied ? <Check className="text-up" /> : <Copy />} {copied ? "Copied" : "Copy codes"}
        </Button>
        <Button variant="outline" size="sm" onClick={download}>
          <Download /> Download .txt
        </Button>
      </div>
    </div>
  );
}
