UPDATE "dz-menu-modifiers" SET ingredient_code='ING-YOGURT', extra_qty_grams=-80
WHERE name='Yoğurtsuz' AND menu_item_id=(SELECT id FROM "dz-menu-items" WHERE code='MN-ISKENDER') AND COALESCE(ingredient_code,'')='';
