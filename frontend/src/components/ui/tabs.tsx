import * as React from "react"
import * as TabsPrimitive from "@radix-ui/react-tabs"
import { cn } from "@/lib/utils"

const Tabs = TabsPrimitive.Root

function TabsList({ className, ...props }: React.ComponentProps<typeof TabsPrimitive.List>) {
  return <TabsPrimitive.List className={cn("bg-muted/60 text-muted-foreground inline-flex h-9 w-fit items-center justify-center rounded-lg p-[3px]", className)} {...props} />
}

function TabsTrigger({ className, ...props }: React.ComponentProps<typeof TabsPrimitive.Trigger>) {
  return (
    <TabsPrimitive.Trigger
      className={cn("data-[state=active]:bg-background data-[state=active]:text-foreground focus-visible:ring-ring/40 inline-flex h-full flex-1 items-center justify-center gap-1.5 rounded-md border border-transparent px-3 py-1 text-[13px] font-medium whitespace-nowrap transition-all focus-visible:ring-[3px] focus-visible:outline-none disabled:pointer-events-none disabled:opacity-50 data-[state=active]:shadow-sm [&_svg]:size-3.5", className)}
      {...props}
    />
  )
}

function TabsContent({ className, ...props }: React.ComponentProps<typeof TabsPrimitive.Content>) {
  return <TabsPrimitive.Content className={cn("mt-3 outline-none", className)} {...props} />
}

export { Tabs, TabsContent, TabsList, TabsTrigger }
