"""The demo marketplace's fixed shops, catalogs and buyers. Immutable: there
is nothing to create or edit, the demo is about the checkout."""

from demoshop.catalog.schemas import Customer, Product, Shop

# Each shop is a tenant of the platform: it uploads its own scripts and
# activates its own versions on checkout.discount, independently of the others.
SHOPS: tuple[Shop, ...] = (
    Shop(id="merchant-a", name="Чайная лавка", description="Чай, посуда и мёд."),
    Shop(id="merchant-b", name="Кофейня на углу", description="Кофе в зёрнах и всё для варки."),
)

PRODUCTS: tuple[Product, ...] = (
    Product(
        id="tea-sencha",
        shop_id="merchant-a",
        name="Чай сенча",
        description="Зелёный японский чай, 100 г.",
        price=45000,
    ),
    Product(
        id="teapot-glass",
        shop_id="merchant-a",
        name="Стеклянный чайник",
        description="Жаропрочное стекло, 600 мл.",
        price=210000,
    ),
    Product(
        id="honey-linden",
        shop_id="merchant-a",
        name="Липовый мёд",
        description="Алтайский мёд, 500 г.",
        price=65000,
    ),
    Product(
        id="coffee-ethiopia",
        shop_id="merchant-b",
        name="Кофе Эфиопия",
        description="Зерно средней обжарки, 250 г.",
        price=89000,
    ),
    Product(
        id="cezve-copper",
        shop_id="merchant-b",
        name="Медная турка",
        description="Ручная чеканка, 300 мл.",
        price=180000,
    ),
    Product(
        id="mug-ceramic",
        shop_id="merchant-b",
        name="Керамическая кружка",
        description="Ручная работа, 350 мл.",
        price=120000,
    ),
)

# The discount template (examples/scripts/rust/discount) gives 10 % from
# 1000 rubles of past purchases by default: buyers on both sides of it.
CUSTOMERS: tuple[Customer, ...] = (
    Customer(id="c-new", name="Новый покупатель", lifetime_spend=0),
    Customer(id="c-occasional", name="Случайный покупатель", lifetime_spend=80000),
    Customer(id="c-loyal", name="Постоянный покупатель", lifetime_spend=240000),
)
