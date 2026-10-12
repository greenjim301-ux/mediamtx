import * as React from "react";
import * as D from "@radix-ui/react-dialog";
import { X } from "lucide-react";
import { cn } from "@/lib/utils";
export const Dialog = D.Root;
export const DialogTitle = D.Title;
export const DialogDescription = D.Description;
export const DialogContent = React.forwardRef<
  React.ElementRef<typeof D.Content>,
  React.ComponentPropsWithoutRef<typeof D.Content>
>(({ className, children, ...props }, ref) => (
  <D.Portal>
    <D.Overlay className="fixed inset-0 z-50 bg-slate-950/55 backdrop-blur-sm" />
    <D.Content
      ref={ref}
      className={cn(
        "fixed left-1/2 top-1/2 z-50 max-h-[90vh] w-[calc(100%-2rem)] max-w-lg -translate-x-1/2 -translate-y-1/2 overflow-y-auto rounded-2xl border bg-background p-6 shadow-2xl",
        className,
      )}
      {...props}
    >
      {children}
      <D.Close
        className="absolute right-4 top-4 rounded p-1 text-muted-foreground hover:bg-accent focus-visible:ring-2"
        aria-label="关闭"
      >
        <X size={18} />
      </D.Close>
    </D.Content>
  </D.Portal>
));
DialogContent.displayName = "DialogContent";
