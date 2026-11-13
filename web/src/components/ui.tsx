import React, { useEffect, useRef } from 'react';
import * as SwitchPrimitive from '@radix-ui/react-switch';
import * as TabsPrimitive from '@radix-ui/react-tabs';
import * as DialogPrimitive from '@radix-ui/react-dialog';
import { cva, type VariantProps } from 'class-variance-authority';
import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';
import {
  HTMLMotionProps,
  LayoutGroup,
  motion,
  useMotionValue,
  useSpring,
} from 'motion/react';

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

/**
 * Authentic iOS 18 CoreAnimation spring parameters:
 * High responsiveness, zero entry lag, smooth interruption.
 */
export const IOS_SPRING = {
  type: 'spring' as const,
  stiffness: 520,
  damping: 32,
  mass: 0.55,
};

export const IOS_SOFT_SPRING = {
  type: 'spring' as const,
  stiffness: 440,
  damping: 30,
  mass: 0.65,
};

export const IOS_BOUNCE_SPRING = {
  type: 'spring' as const,
  stiffness: 680,
  damping: 22,
  mass: 0.45,
};

/**
 * Hook that drives a continuous, mid-flight interruptible iOS spring transition
 * whenever `triggerKey` changes — WITHOUT unmounting or snapping opacity to 0.
 */
export function useInterruptibleViewSpring(triggerKey: string | number) {
  const prevKeyRef = useRef<string | number>(triggerKey);
  const rawY = useMotionValue(0);
  const rawScale = useMotionValue(1);
  const rawOpacity = useMotionValue(1);

  const y = useSpring(rawY, { stiffness: 520, damping: 34, mass: 0.5 });
  const scale = useSpring(rawScale, { stiffness: 560, damping: 32, mass: 0.48 });
  const opacity = useSpring(rawOpacity, { stiffness: 480, damping: 35, mass: 0.4 });

  useEffect(() => {
    if (prevKeyRef.current !== triggerKey) {
      prevKeyRef.current = triggerKey;
      // Inject instantaneous impulse into existing spring without destroying velocity
      rawY.jump(10);
      rawScale.jump(0.982);
      rawOpacity.jump(0.72);

      rawY.set(0);
      rawScale.set(1);
      rawOpacity.set(1);
    }
  }, [triggerKey, rawY, rawScale, rawOpacity]);

  return { y, scale, opacity };
}

const buttonVariants = cva(
  'inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-xl font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500/40 disabled:pointer-events-none disabled:opacity-50 cursor-pointer select-none shrink-0',
  {
    variants: {
      variant: {
        default:
          'bg-zinc-900 text-white shadow-xs hover:bg-zinc-800 active:bg-zinc-700 dark:bg-white dark:text-zinc-950 dark:hover:bg-zinc-100',
        accent:
          'bg-[#007AFF] text-white shadow-xs hover:bg-[#006FE6] active:bg-[#005FCC] dark:bg-[#0A84FF] dark:text-white dark:hover:bg-[#007AFF]',
        secondary:
          'bg-zinc-100 text-zinc-900 hover:bg-zinc-200/80 active:bg-zinc-300/70 dark:bg-zinc-800/90 dark:text-zinc-100 dark:hover:bg-zinc-700/80',
        outline:
          'border border-zinc-200/90 bg-white/90 text-zinc-700 shadow-2xs hover:bg-zinc-50 hover:text-zinc-950 active:bg-zinc-100 dark:border-zinc-800 dark:bg-zinc-900/70 dark:text-zinc-200 dark:hover:bg-zinc-800/80 dark:hover:text-white',
        ghost:
          'text-zinc-600 hover:bg-zinc-200/60 hover:text-zinc-900 active:bg-zinc-200 dark:text-zinc-400 dark:hover:bg-zinc-800/70 dark:hover:text-zinc-100',
        destructive:
          'bg-[#FF3B30] text-white shadow-xs hover:bg-[#E0352B] active:bg-[#C92E24] dark:bg-[#FF453A] dark:text-white dark:hover:bg-[#FF3B30]',
      },
      size: {
        default: 'h-9 px-3.5 py-2 text-xs',
        sm: 'h-8 rounded-lg px-2.5 text-xs',
        lg: 'h-10 rounded-xl px-4 text-sm',
        icon: 'h-8 w-8 rounded-lg',
      },
    },
    defaultVariants: {
      variant: 'default',
      size: 'default',
    },
  }
);

export interface ButtonProps
  extends Omit<HTMLMotionProps<'button'>, 'children'>,
    VariantProps<typeof buttonVariants> {
  children?: React.ReactNode;
}

/**
 * Tactile iOS Button with continuous velocity-preserving spring scale & y-impulse.
 * Even a 20ms tap produces a visible elastic press-and-rebound curve, and rapid
 * clicks interrupt mid-flight smoothly.
 */
export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, children, disabled, onPointerDown, onPointerUp, onPointerLeave, ...props }, ref) => {
    const rawScale = useMotionValue(1);
    const rawY = useMotionValue(0);
    const scale = useSpring(rawScale, { stiffness: 720, damping: 20, mass: 0.42 });
    const y = useSpring(rawY, { stiffness: 720, damping: 22, mass: 0.42 });
    const releaseTimerRef = useRef<number | null>(null);

    useEffect(() => {
      return () => {
        if (releaseTimerRef.current) window.clearTimeout(releaseTimerRef.current);
      };
    }, []);

    const handlePointerDown = (e: React.PointerEvent<HTMLButtonElement>) => {
      if (!disabled) {
        if (releaseTimerRef.current) window.clearTimeout(releaseTimerRef.current);
        rawScale.set(0.89);
        rawY.set(1.5);
      }
      onPointerDown?.(e);
    };

    const triggerSpringRelease = () => {
      if (disabled) return;
      if (releaseTimerRef.current) window.clearTimeout(releaseTimerRef.current);
      // Ensure even a 15ms fast click reaches a visible squish depth before rebounding with overshoot
      releaseTimerRef.current = window.setTimeout(() => {
        rawScale.set(1);
        rawY.set(0);
      }, 65);
    };

    const handlePointerUp = (e: React.PointerEvent<HTMLButtonElement>) => {
      triggerSpringRelease();
      onPointerUp?.(e);
    };

    const handlePointerLeave = (e: React.PointerEvent<HTMLButtonElement>) => {
      if (!disabled) {
        rawScale.set(1);
        rawY.set(0);
      }
      onPointerLeave?.(e);
    };

    return (
      <motion.button
        ref={ref}
        style={{ scale, y }}
        whileHover={disabled ? undefined : { scale: 1.025 }}
        transition={IOS_BOUNCE_SPRING}
        disabled={disabled}
        onPointerDown={handlePointerDown}
        onPointerUp={handlePointerUp}
        onPointerLeave={handlePointerLeave}
        className={cn(buttonVariants({ variant, size, className }))}
        {...props}
      >
        {children}
      </motion.button>
    );
  }
);
Button.displayName = 'Button';

export interface SwitchProps {
  id?: string;
  checked: boolean;
  onCheckedChange: (checked: boolean) => void;
  ariaLabel?: string;
  disabled?: boolean;
}

export const Switch: React.FC<SwitchProps> = ({
  id,
  checked,
  onCheckedChange,
  ariaLabel,
  disabled = false,
}) => {
  const rawScale = useMotionValue(1);
  const scale = useSpring(rawScale, { stiffness: 680, damping: 20, mass: 0.4 });

  return (
    <SwitchPrimitive.Root
      id={id}
      checked={checked}
      onCheckedChange={(next) => {
        rawScale.jump(0.9);
        rawScale.set(1);
        onCheckedChange(next);
      }}
      aria-label={ariaLabel}
      disabled={disabled}
      asChild
    >
      <motion.button
        type="button"
        style={{ scale }}
        whileTap={disabled ? undefined : { scale: 0.92 }}
        transition={IOS_BOUNCE_SPRING}
        className={cn(
          'peer inline-flex h-[28px] w-[48px] shrink-0 cursor-pointer items-center rounded-full p-[2px] transition-colors duration-200 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500/40 disabled:cursor-not-allowed disabled:opacity-50',
          checked
            ? 'bg-[#34C759] dark:bg-[#30D158] justify-end'
            : 'bg-zinc-200 dark:bg-zinc-700 justify-start'
        )}
      >
        <SwitchPrimitive.Thumb asChild>
          <motion.span
            layout
            whileTap={{ width: 28 }}
            transition={{
              type: 'spring',
              stiffness: 580,
              damping: 30,
              mass: 0.5,
            }}
            className="pointer-events-none block h-[24px] w-[24px] rounded-full bg-white shadow-md ring-0"
          />
        </SwitchPrimitive.Thumb>
      </motion.button>
    </SwitchPrimitive.Root>
  );
};

export interface SegmentedTabsProps<T extends string> {
  value: T;
  onValueChange: (val: T) => void;
  items: { id: T; label: React.ReactNode; icon?: React.ReactNode }[];
  layoutGroupId: string;
  ariaLabel?: string;
  className?: string;
}

export function SegmentedTabs<T extends string>({
  value,
  onValueChange,
  items,
  layoutGroupId,
  ariaLabel,
  className,
}: SegmentedTabsProps<T>) {
  return (
    <LayoutGroup id={layoutGroupId}>
      <TabsPrimitive.Root
        value={value}
        onValueChange={(v) => onValueChange(v as T)}
        className={className}
      >
        <TabsPrimitive.List
          aria-label={ariaLabel}
          className="inline-flex flex-wrap items-center gap-1 p-1 rounded-xl bg-zinc-200/70 dark:bg-zinc-800/80 backdrop-blur-md"
        >
          {items.map((item) => {
            const isActive = value === item.id;
            return (
              <TabsPrimitive.Trigger key={item.id} value={item.id} asChild>
                <motion.button
                  type="button"
                  whileTap={{ scale: 0.91 }}
                  whileHover={{ scale: 1.02 }}
                  transition={IOS_BOUNCE_SPRING}
                  className={cn(
                    'relative h-7 px-3 rounded-lg text-xs font-medium inline-flex items-center justify-center gap-1.5 transition-colors cursor-pointer select-none whitespace-nowrap z-10 focus-visible:outline-none',
                    isActive
                      ? 'text-zinc-950 dark:text-white font-semibold'
                      : 'text-zinc-600 dark:text-zinc-400 hover:text-zinc-950 dark:hover:text-zinc-200'
                  )}
                >
                  {isActive && (
                    <motion.span
                      layoutId={`${layoutGroupId}-pill`}
                      transition={IOS_SPRING}
                      className="absolute inset-0 rounded-lg bg-white dark:bg-zinc-900 shadow-xs -z-10"
                    />
                  )}
                  {item.icon}
                  <span>{item.label}</span>
                </motion.button>
              </TabsPrimitive.Trigger>
            );
          })}
        </TabsPrimitive.List>
      </TabsPrimitive.Root>
    </LayoutGroup>
  );
}

export interface InputProps extends React.InputHTMLAttributes<HTMLInputElement> {}

export const Input = React.forwardRef<HTMLInputElement, InputProps>(
  ({ className, ...props }, ref) => (
    <input
      ref={ref}
      className={cn(
        'flex h-9 w-full rounded-xl border border-zinc-200/90 bg-white px-3 py-1.5 text-sm text-zinc-900 shadow-2xs transition-colors placeholder:text-zinc-400 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#007AFF]/35 focus-visible:border-[#007AFF] disabled:cursor-not-allowed disabled:opacity-60 dark:border-zinc-800 dark:bg-zinc-900/70 dark:text-zinc-100 dark:placeholder:text-zinc-500',
        className
      )}
      {...props}
    />
  )
);
Input.displayName = 'Input';

export interface SelectProps extends React.SelectHTMLAttributes<HTMLSelectElement> {}

export const Select = React.forwardRef<HTMLSelectElement, SelectProps>(
  ({ className, children, ...props }, ref) => (
    <select
      ref={ref}
      className={cn(
        'flex h-9 rounded-xl border border-zinc-200/90 bg-white px-3 py-1.5 text-xs font-medium text-zinc-800 shadow-2xs transition-colors cursor-pointer focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#007AFF]/35 focus-visible:border-[#007AFF] disabled:cursor-not-allowed disabled:opacity-60 dark:border-zinc-800 dark:bg-zinc-900/70 dark:text-zinc-100',
        className
      )}
      {...props}
    >
      {children}
    </select>
  )
);
Select.displayName = 'Select';

export { DialogPrimitive };
