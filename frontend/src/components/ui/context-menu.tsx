import * as React from "react"
import * as ContextMenuPrimitive from "@radix-ui/react-context-menu"
import { Check } from "lucide-react"
import { cn } from "@/lib/utils"

const ContextMenu = ContextMenuPrimitive.Root
const ContextMenuTrigger = ContextMenuPrimitive.Trigger
const ContextMenuGroup = ContextMenuPrimitive.Group

function ContextMenuContent({ className, ...props }: React.ComponentProps<typeof ContextMenuPrimitive.Content>) {
  return (
    <ContextMenuPrimitive.Portal>
      <ContextMenuPrimitive.Content
        className={cn("bg-popover text-popover-foreground z-50 min-w-[11rem] overflow-hidden rounded-lg border border-border/60 p-1 shadow-lg", className)}
        {...props}
      />
    </ContextMenuPrimitive.Portal>
  )
}

function ContextMenuItem({ className, inset, ...props }: React.ComponentProps<typeof ContextMenuPrimitive.Item> & { inset?: boolean }) {
  return (
    <ContextMenuPrimitive.Item
      data-inset={inset}
      className={cn("focus:bg-accent focus:text-accent-foreground relative flex cursor-default items-center gap-2 rounded-md px-2 py-1.5 text-[12.5px] outline-none select-none data-[disabled]:pointer-events-none data-[disabled]:opacity-50 data-[inset]:pl-8 [&_svg]:size-3.5 [&_svg]:shrink-0", className)}
      {...props}
    />
  )
}

function ContextMenuLabel({ className, ...props }: React.ComponentProps<typeof ContextMenuPrimitive.Label>) {
  return <ContextMenuPrimitive.Label className={cn("px-2 py-1 text-[10px] font-medium tracking-wide text-muted-foreground", className)} {...props} />
}

function ContextMenuSeparator({ className, ...props }: React.ComponentProps<typeof ContextMenuPrimitive.Separator>) {
  return <ContextMenuPrimitive.Separator className={cn("bg-border -mx-1 my-1 h-px", className)} {...props} />
}

function ContextMenuCheckboxItem({ className, children, checked, ...props }: React.ComponentProps<typeof ContextMenuPrimitive.CheckboxItem>) {
  return (
    <ContextMenuPrimitive.CheckboxItem
      className={cn("focus:bg-accent focus:text-accent-foreground relative flex cursor-default items-center gap-2 rounded-md py-1.5 pr-2 pl-8 text-[12.5px] outline-none select-none", className)}
      checked={checked}
      {...props}
    >
      <span className="absolute left-2 flex size-3.5 items-center justify-center">
        <ContextMenuPrimitive.ItemIndicator><Check className="size-3.5" /></ContextMenuPrimitive.ItemIndicator>
      </span>
      {children}
    </ContextMenuPrimitive.CheckboxItem>
  )
}

export { ContextMenu, ContextMenuCheckboxItem, ContextMenuContent, ContextMenuGroup, ContextMenuItem, ContextMenuLabel, ContextMenuSeparator, ContextMenuTrigger }
